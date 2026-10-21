// Command server runs the user service: load config, open and migrate the database, serve until SIGINT/SIGTERM.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/cors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	allowedadmin "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/admin"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/bootstrap"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/alloweddomaindb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/warningdb"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	userservicejwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	warningdomain "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/warning"
	"gorm.io/gorm"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
	SHUTDOWN_TIMEOUT_SEC    = 10
)

type config struct {
	port            string
	databaseURL     string
	dbMaxOpen       int
	dbMaxIdle       int
	runMigrations   bool
	appEnv          string
	frontendURL     string
	privateKey      string
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	smtp            email.SMTPSender
	bootstrap       bootstrap.Config
	kafkaBrokers    []string
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "user-service")
	if err := run(log); err != nil {
		log.Error("user service stopped", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, rootCancel := context.WithCancel(signalCtx)
	defer rootCancel()

	db, err := database.Open(cfg.databaseURL, cfg.dbMaxOpen, cfg.dbMaxIdle)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("database handle: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	if cfg.runMigrations {
		if err := database.Migrate(db); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		log.Info("migrations applied")
	}

	if cfg.bootstrap.Email != "" {
		admin, outcome, err := (&bootstrap.Bootstrapper{Store: store.NewAdmin(db)}).Run(ctx, cfg.bootstrap)
		if err != nil {
			return fmt.Errorf("admin bootstrap: %w", err)
		}
		if outcome == store.BootstrapAlreadyDone {
			log.Info("admin already bootstrapped; BOOTSTRAP_SUPERADMIN_EMAIL ignored")
		} else {
			log.Info("admin bootstrapped", "outcome", outcome.String(), "user_id", admin.ID)
		}
	}

	allowedDomainLogic := newAllowedDomainService(sqlDB)
	auth, codec, persistence, err := newAuthServices(db, cfg, allowedDomainLogic)
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}
	warningLogic := newWarningService(sqlDB)
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{Brokers: cfg.kafkaBrokers, GroupID: "user-service"})
	if err != nil {
		return fmt.Errorf("configure Kafka subscriber: %w", err)
	}
	if err := warningdomain.RegisterCourierWarningConsumer(subscriber, &warningdomain.CourierWarningConsumer{Logic: warningLogic}); err != nil {
		return fmt.Errorf("register courier warning consumer: %w", err)
	}
	profileLogic := &service.ProfileService{Users: persistence.Users}
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
		Warnings:       &warningdomain.Handler{Logic: warningLogic},
		Authenticate:   httpauth.AuthenticateLocal(codec.VerifyAccess),
		Users:          persistence.Users,
	}
	handler := getCorsConfig(auth.AllowedOrigin).Handler(router.Setup(&health.Handler{DB: sqlDB}, auth, protected))
	srv := newServer(":"+cfg.port, handler)

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		errCh <- srv.ListenAndServe()
	}()
	kafkaErrCh := make(chan error, 1)
	go func() { kafkaErrCh <- subscriber.Run(ctx) }()

	var runErr error
	subscriberExited := false
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			runErr = fmt.Errorf("serve: %w", err)
		}
	case err := <-kafkaErrCh:
		subscriberExited = true
		if err != nil {
			runErr = fmt.Errorf("Kafka subscriber: %w", err)
		} else if ctx.Err() == nil {
			runErr = errors.New("Kafka subscriber stopped unexpectedly")
		}
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), SHUTDOWN_TIMEOUT_SEC*time.Second)
	defer shutdownCancel()
	stop()
	rootCancel()
	log.Info("shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		runErr = errors.Join(runErr, fmt.Errorf("HTTP shutdown: %w", err))
	}
	if !subscriberExited {
		select {
		case err := <-kafkaErrCh:
			if err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("stop Kafka subscriber: %w", err))
			}
		case <-shutdownCtx.Done():
			runErr = errors.Join(runErr, errors.New("Kafka subscriber did not stop before shutdown deadline"))
		}
	}
	return runErr
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

// loadConfig reads .env (if present) and the environment once, applying
// local defaults and rejecting missing required values or malformed settings.
func loadConfig() (config, error) {
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf("load .env: %w", err)
	}

	var errs []error
	cfg := config{
		port:            envString("PORT", "8080"),
		databaseURL:     envString("DATABASE_URL", ""),
		dbMaxOpen:       envPositiveInt("DB_MAX_OPEN", 10, &errs),
		dbMaxIdle:       envPositiveInt("DB_MAX_IDLE", 5, &errs),
		runMigrations:   envBool("RUN_MIGRATIONS", true, &errs),
		appEnv:          envString("APP_ENV", ""),
		frontendURL:     envString("FRONTEND_BASE_URL", ""),
		privateKey:      envString("JWT_PRIVATE_KEY_FILE", ""),
		accessTokenTTL:  envDuration("JWT_ACCESS_TOKEN_TTL", 0, &errs),
		refreshTokenTTL: envDuration("JWT_REFRESH_TOKEN_TTL", 0, &errs),
		smtp: email.SMTPSender{
			Addr:     envString("SMTP_ADDR", ""),
			From:     envString("SMTP_FROM", ""),
			Username: envString("SMTP_USERNAME", ""),
			Password: os.Getenv("SMTP_PASSWORD"),
			Timeout:  envDuration("SMTP_TIMEOUT", 5*time.Second, &errs),
		},
		bootstrap: bootstrap.Config{
			Email:       envString("BOOTSTRAP_SUPERADMIN_EMAIL", ""),
			DisplayName: envString("BOOTSTRAP_SUPERADMIN_DISPLAY_NAME", ""),
		},
		kafkaBrokers: envList("KAFKA_BROKERS", ""),
	}
	if cfg.frontendURL == "" && cfg.appEnv == "local" {
		cfg.frontendURL = "http://localhost:5173"
	}
	if len(cfg.kafkaBrokers) == 0 && cfg.appEnv == "local" {
		cfg.kafkaBrokers = []string{"localhost:9092"}
	}
	if cfg.privateKey == "" && cfg.appEnv == "local" {
		cfg.privateKey = "../.local/secrets/auth/jwt-signing-private.pem"
	}
	// Local mail goes to Mailpit; `docker compose` overrides the host to mailpit.
	if cfg.smtp.Addr == "" && cfg.appEnv == "local" {
		cfg.smtp.Addr = "localhost:1025"
	}
	if cfg.smtp.From == "" && cfg.appEnv == "local" {
		cfg.smtp.From = "FoC <no-reply@foc.local>"
	}
	if cfg.databaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is not set"))
	}
	if len(cfg.kafkaBrokers) == 0 {
		errs = append(errs, errors.New("KAFKA_BROKERS must contain at least one broker"))
	}
	if cfg.frontendURL == "" {
		errs = append(errs, errors.New("FRONTEND_BASE_URL is not set"))
	}
	if cfg.privateKey == "" {
		errs = append(errs, errors.New("JWT_PRIVATE_KEY_FILE is not set"))
	}
	if cfg.smtp.Addr == "" {
		errs = append(errs, errors.New("SMTP_ADDR is not set"))
	}
	if cfg.smtp.From == "" {
		errs = append(errs, errors.New("SMTP_FROM is not set"))
	} else if _, err := mail.ParseAddress(cfg.smtp.From); err != nil {
		errs = append(errs, fmt.Errorf("SMTP_FROM must be an email address, got %q", cfg.smtp.From))
	}
	return cfg, errors.Join(errs...)
}

func envString(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envList(key, fallback string) []string {
	value := envString(key, fallback)
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func envPositiveInt(key string, fallback int, errs *[]error) int {
	v := envString(key, "")
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive integer, got %q", key, v))
		return fallback
	}
	return n
}

func envBool(key string, fallback bool, errs *[]error) bool {
	v := envString(key, "")
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a boolean, got %q", key, v))
		return fallback
	}
	return b
}

func envDuration(key string, fallback time.Duration, errs *[]error) time.Duration {
	v := envString(key, "")
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a valid duration, got %q", key, v))
		return fallback
	}
	return d
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
		Users: persistence.Users, AuthTokens: persistence.AuthTokens,
		Sessions: persistence.Sessions, DomainPolicy: policy,
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

func newAuthServices(db *gorm.DB, cfg config, domainPolicy service.EmailDomainPolicy) (*authhandler.Handler, *userservicejwt.ES256Codec, *store.Store, error) {
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
		TokenCodec: codec, EmailSender: cfg.smtp,
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
