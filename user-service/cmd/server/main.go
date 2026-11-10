// Command server runs the user service: load config, open and migrate the database, serve until SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	allowedadmin "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/admin"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/bootstrap"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	review "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
	SHUTDOWN_TIMEOUT_SEC    = 10

	warningConsumerGroup     = "user-service"
	minWarningConsumeBackoff = time.Second
	maxWarningConsumeBackoff = 30 * time.Second
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "user-service")
	slog.SetDefault(log)
	fatal := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal("invalid config", err)
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, rootCancel := context.WithCancel(signalCtx)
	defer rootCancel()

	db, err := database.Open(cfg.databaseURL, cfg.dbMaxOpen, cfg.dbMaxIdle)
	if err != nil {
		fatal("open database", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		fatal("database handle", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if cfg.runMigrations {
		if err := database.Migrate(db); err != nil {
			fatal("migrate", err)
		}
		log.Info("migrations applied")
	}

	if cfg.bootstrap.Email != "" {
		admin, outcome, err := (&bootstrap.Bootstrapper{Store: store.NewAdmin(db)}).Run(ctx, cfg.bootstrap)
		if err != nil {
			fatal("admin bootstrap", err)
		}
		if outcome == store.BootstrapAlreadyDone {
			log.Info("admin already bootstrapped; BOOTSTRAP_SUPERADMIN_EMAIL ignored")
		} else {
			log.Info("admin bootstrapped", "outcome", outcome.String(), "user_id", admin.ID)
		}
	}

	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: cfg.kafkaBrokers})
	if err != nil {
		fatal("initialize Kafka publisher", err)
	}
	defer func() {
		if err := publisher.Close(); err != nil {
			log.Error("close Kafka publisher", "err", err)
		}
	}()

	allowedDomainLogic := newAllowedDomainService(sqlDB)
	auth, codec, persistence, err := newAuthServices(db, cfg, allowedDomainLogic)
	if err != nil {
		fatal("initialize authentication", err)
	}
	warningLogic := newWarningService(sqlDB)
	reviewRepo := review.NewPostgresRepository(reviewdb.New(sqlDB))
	reviewLogic, imageStorage := newReviewService(sqlDB, cfg)
	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stopWorkers(); workers.Wait() }()
	workers.Go(func() { outbox.New(persistence.UserCreatedOutbox, publisher).Run(workerCtx) })
	workers.Go(func() { review.NewOutboxRelay(reviewRepo, publisher).Run(workerCtx) })
	completions := review.NewCompletionHandler(reviewRepo, reviewTransactions(sqlDB))
	workers.Go(func() { consumeReviewModeration(workerCtx, cfg.kafkaBrokers, completions) })
	workers.Go(func() { consume(workerCtx, cfg.kafkaBrokers, warningLogic) })

	profileLogic := &service.ProfileService{
		Users:    persistence.Users,
		Pictures: persistence.ProfilePictures,
		Storage:  profilePictureStorage(imageStorage),
	}
	roleLogic := &service.RoleService{
		Users: persistence.Users,
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return persistence.WithTransaction(ctx, func(tx *store.Store) error {
				return operation(service.RoleStore{Users: tx.Users, Admin: tx.Admin})
			})
		},
	}
	protected := router.ProtectedRoutes{
		Profile:        &profilehandler.Handler{Logic: profileLogic},
		Admin:          &adminhandler.Handler{Logic: roleLogic},
		AllowedDomains: &allowedadmin.AllowedDomainHandler{Logic: allowedDomainLogic},
		Review:         &review.Handler{Logic: reviewLogic},
		Warnings:       &warningdomain.Handler{Logic: warningLogic},
		AdminWarnings:  &warningdomain.AdminHandler{Logic: warningLogic},
		Authenticate:   httpauth.AuthenticateLocal(codec.VerifyAccess),
		Users:          persistence.Users,
	}
	cors := getCorsConfig(auth.AllowedOrigin)
	// Every service runs the listener split: public routes on PORT, private
	// routes on INTERNAL_PORT. Services without private routes yet still bind
	// the internal listener so local and deployed behaviour stay identical.
	public := newServer(":"+cfg.port, cors.Handler(router.Public(&health.Handler{DB: sqlDB}, auth, protected)))
	internal := newServer(":"+cfg.internalPort, router.Internal(auth))
	servers := []*http.Server{public, internal}

	if imageStorage != nil {
		// Orphan review photos and profile pictures are reaped on a minute
		// ticker; both domains' claim/delete steps are retryable and idempotent.
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if err := reviewLogic.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
					slog.WarnContext(ctx, "review photo cleanup deferred", "err", err)
				}
				if err := profileLogic.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
					slog.WarnContext(ctx, "profile picture cleanup deferred", "err", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}

	if err := serve(ctx, servers...); err != nil {
		fatal("serve", err)
	}
	stop()
	rootCancel()
	stopWorkers()
	workers.Wait()
}

// serve runs every server until ctx ends or one of them stops, then shuts
// them all down.
func serve(ctx context.Context, servers ...*http.Server) error {
	stopped := make(chan error, len(servers))
	for _, server := range servers {
		go func() { stopped <- server.ListenAndServe() }()
		slog.Info("listening", "addr", server.Addr)
	}

	running := len(servers)
	var errs []error
	select {
	case err := <-stopped:
		running--
		errs = append(errs, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), SHUTDOWN_TIMEOUT_SEC*time.Second)
	defer cancel()

	for _, server := range servers {
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			errs = append(errs, fmt.Errorf("shutdown server %s: %w", server.Addr, err))
		}
	}
	for range running {
		errs = append(errs, <-stopped)
	}

	var failures []error
	for _, err := range errs {
		if !errors.Is(err, http.ErrServerClosed) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// consume runs the courier-warning subscription until ctx ends. A failed
// subscriber cannot restart, so each failure builds a new one after a growing
// delay; the uncommitted delivery is then redelivered.
func consume(ctx context.Context, brokers []string, logic *warningdomain.Service) {
	backoff := minWarningConsumeBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := subscribe(ctx, brokers, logic)
		if ctx.Err() != nil {
			return
		}

		// A subscription that ran for a while was healthy; restart promptly.
		if time.Since(started) > maxWarningConsumeBackoff {
			backoff = minWarningConsumeBackoff
		}
		slog.ErrorContext(ctx, "Kafka subscription stopped; restarting", "error", err, "backoff", backoff)

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(2*backoff, maxWarningConsumeBackoff)
	}
}

func subscribe(ctx context.Context, brokers []string, logic *warningdomain.Service) error {
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{Brokers: brokers, GroupID: warningConsumerGroup})
	if err != nil {
		return err
	}
	if err := warningdomain.RegisterCourierWarningConsumer(
		subscriber,
		&warningdomain.CourierWarningConsumer{Logic: logic},
	); err != nil {
		return err
	}
	return subscriber.Run(ctx)
}

func newServer(addr string, handler http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: READ_HEADER_TIMEOUT_SEC * time.Second,
		Protocols:         protocols,
	}
}
