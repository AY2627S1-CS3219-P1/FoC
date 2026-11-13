// Command server runs the user service: load config, open and migrate the database, serve until SIGINT/SIGTERM.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rs/cors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/revocation"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	allowedadmin "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/admin"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/bootstrap"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/alloweddomaindb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	userservicejwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	review "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
	"gorm.io/gorm"
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

	// The revocation dictionary TTL must match the effective access-token
	// lifetime; EffectiveAccessTokenLifetime applies the same default NewService
	// applies when the TTL is unset.
	accessTTL := service.EffectiveAccessTokenLifetime(cfg.accessTokenTTL)
	revocations, err := revocation.NewStore(ctx, revocation.Config{
		Address: cfg.revocationServiceURL, TTL: accessTTL,
	})
	if err != nil {
		fatal("initialize access-token revocation store", err)
	}
	defer func() { _ = revocations.Close() }()

	// Periodically delete expired revocation entries so the table and its
	// index stay bounded; reads already ignore expired entries, this only
	// reclaims space. One clean-up per TTL keeps the sweep proportional to
	// the write rate.
	cleanupCtx, stopCleanup := context.WithCancel(ctx)
	defer stopCleanup()
	go func() {
		ticker := time.NewTicker(accessTTL)
		defer ticker.Stop()
		for {
			select {
			case <-cleanupCtx.Done():
				return
			case <-ticker.C:
				deleted, err := revocations.DeleteExpired(cleanupCtx)
				if err != nil {
					log.Warn("delete expired revocation entries", "err", err)
					continue
				}
				if deleted > 0 {
					log.Info("deleted expired revocation entries", "count", deleted)
				}
			}
		}
	}()

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
		admin, outcome, err := (&bootstrap.Bootstrapper{
			Store: store.NewAdmin(db), Revocations: revocations,
		}).Run(ctx, cfg.bootstrap)
		if err != nil {
			fatal("admin bootstrap", err)
		}
		switch outcome {
		case store.BootstrapAlreadyDone:
			log.Info("admin already bootstrapped; BOOTSTRAP_SUPERADMIN_EMAIL ignored")
		case store.BootstrapUnchanged:
			log.Info("admin is already a super admin; BOOTSTRAP_SUPERADMIN_EMAIL ignored")
		default:
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
	auth, codec, persistence, err := newAuthServices(db, cfg, allowedDomainLogic, revocations)
	if err != nil {
		fatal("initialize authentication", err)
	}
	warningLogic := newWarningService(sqlDB)
	relay := outbox.New(persistence.UserCreatedOutbox, publisher)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		relay.Run(ctx)
	}()

	// Reconcile the revocation store against the authoritative sessions
	// table: republish sessions revoked within the access-token TTL whose
	// dictionary entry is missing because a publication failed or the
	// process crashed between commit and publish.
	go reconcileRevocations(cleanupCtx, persistence.Sessions, revocations, accessTTL, log)

	profileLogic := &service.ProfileService{Users: persistence.Users}
	roleLogic := &service.RoleService{
		Users: persistence.Users,
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return persistence.WithTransaction(ctx, func(tx *store.Store) error {
				return operation(service.RoleStore{Users: tx.Users, Admin: tx.Admin, Sessions: tx.Sessions})
			})
		},
		Revocations: revocations,
	}
	reviewLogic := newReviewService(sqlDB)
	protected := router.ProtectedRoutes{
		Profile:        &profilehandler.Handler{Logic: profileLogic},
		Admin:          &adminhandler.Handler{Logic: roleLogic},
		AllowedDomains: &allowedadmin.AllowedDomainHandler{Logic: allowedDomainLogic},
		Review:         &review.Handler{Logic: reviewLogic},
		Warnings:       &warningdomain.Handler{Logic: warningLogic},
		AdminWarnings:  &warningdomain.AdminHandler{Logic: warningLogic},
		Authenticate:   httpauth.AuthenticateLocal(codec.VerifyAccess, revocations),
		Users:          persistence.Users,
	}
	cors := getCorsConfig(auth.AllowedOrigin)
	// Every service runs the listener split: public routes on PORT, private
	// routes on INTERNAL_PORT. Services without private routes yet still bind
	// the internal listener so local and deployed behaviour stay identical.
	public := newServer(":"+cfg.port, cors.Handler(router.Public(&health.Handler{DB: sqlDB}, auth, protected)))
	internal := newServer(":"+cfg.internalPort, router.Internal(auth))

	// The courier-warning consumer runs alongside the HTTP listeners and
	// rebuilds its subscription after a transient failure, so one bad delivery
	// cannot stall warnings. rootCancel() stops it during shutdown.
	consumeDone := make(chan struct{})
	go func() {
		defer close(consumeDone)
		consume(ctx, cfg.kafkaBrokers, warningLogic)
	}()

	if err := serve(ctx, public, internal); err != nil {
		fatal("serve", err)
	}
	stop()
	rootCancel()
	<-consumeDone
	<-relayDone
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

func newReviewService(sqlDB *sql.DB) *review.Service {
	repo := review.NewPostgresRepository(reviewdb.New(sqlDB))
	return review.NewService(repo, func(ctx context.Context, operation func(*reviewdb.Queries) error) error {
		tx, err := sqlDB.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback() }()
		if err := operation(reviewdb.New(tx)); err != nil {
			return err
		}
		return tx.Commit()
	})
}

// reconcileRevocations republishes sessions revoked within the retention
// window until ctx ends: once at startup so a restart heals any crash window,
// then once per minute so a failed publication is repaired within the
// revocation NFR's one-minute bound.
func reconcileRevocations(
	ctx context.Context,
	source service.RevocationSource,
	revocations service.AccessTokenRevocations,
	retention time.Duration,
	log *slog.Logger,
) {
	const interval = time.Minute
	publish := func() {
		published, err := service.ReconcileRevocations(ctx, source, revocations, retention)
		switch {
		case err != nil:
			log.Warn("reconcile revoked sessions", "err", err)
		case published > 0:
			log.Info("republished revoked sessions", "count", published)
		}
	}
	publish()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publish()
		}
	}
}

func newAllowedDomainService(db alloweddomaindb.DBTX) *allowedadmin.AllowedDomainService {
	var repo allowedadmin.AllowedDomainRepository = allowedadmin.NewPostgresAllowedAdminRepository(alloweddomaindb.New(db))
	return allowedadmin.NewAllowedDomainService(repo)
}

func newWarningService(db warningdb.DBTX) *warningdomain.Service {
	return warningdomain.NewService(warningdomain.NewPostgresRepository(warningdb.New(db)))
}

func authResources(persistence *store.Store, policy service.EmailDomainPolicy) service.Resources {
	return service.Resources{
		Users:             persistence.Users,
		AuthTokens:        persistence.AuthTokens,
		Sessions:          persistence.Sessions,
		DomainPolicy:      policy,
		UserCreatedEvents: persistence.UserCreatedOutbox,
	}
}

func authTransactions(db *gorm.DB) service.WithTransaction {
	return func(ctx context.Context, operation func(service.Resources) error) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			// Bind sqlc and GORM to the same transaction, without acquiring another connection.
			policy := newAllowedDomainService(tx.Statement.ConnPool)
			return operation(authResources(store.New(tx), policy))
		})
	}
}

func newAuthServices(
	db *gorm.DB,
	cfg config,
	domainPolicy service.EmailDomainPolicy,
	revocations service.AccessTokenRevocations,
) (*authhandler.Handler, *userservicejwt.ES256Codec, *store.Store, error) {
	frontendURL, err := url.Parse(cfg.frontendURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse FRONTEND_BASE_URL: %w", err)
	}
	key, err := userservicejwt.LoadPrivateKeyPEM(cfg.privateKey)
	if err != nil {
		return nil, nil, nil, err
	}
	kid, err := keyID(&key.PublicKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode JWT public key: %w", err)
	}
	codec, err := userservicejwt.NewES256Codec(key, kid,
		httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure JWT signing: %w", err)
	}
	persistence := store.New(db)

	logic, err := service.NewService(service.Dependencies{
		Resources: authResources(persistence, domainPolicy), WithTransaction: authTransactions(db),
		TokenCodec: codec, EmailSender: cfg.smtp, Revocations: revocations,
	}, service.Config{
		FrontendBaseURL: *frontendURL, LocalDevelopment: cfg.appEnv == "local",
		AccessTokenTTL: cfg.accessTokenTTL, RefreshTokenTTL: cfg.refreshTokenTTL,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure auth service: %w", err)
	}
	allowedOrigin := frontendURL.Scheme + "://" + frontendURL.Host
	return &authhandler.Handler{Logic: logic, AllowedOrigin: allowedOrigin}, codec, persistence, nil
}

func keyID(key *ecdsa.PublicKey) (string, error) {
	point, err := key.Bytes()
	if err != nil {
		return "", err
	}
	fingerprint := sha256.Sum256(point)
	return hex.EncodeToString(fingerprint[:]), nil
}

func getCorsConfig(allowedOrigin string) *cors.Cors {
	return cors.New(cors.Options{
		AllowedOrigins:   []string{allowedOrigin},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{
			"Authorization",
			"Content-Type",
			"Connect-Protocol-Version",
			"Connect-Timeout-Ms",
			"Grpc-Timeout",
			"X-Grpc-Web",
			"X-User-Agent",
		},
		ExposedHeaders: []string{
			"Grpc-Status",
			"Grpc-Message",
			"Grpc-Status-Details-Bin",
		},
	})
}
