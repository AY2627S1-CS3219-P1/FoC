// Command server runs the user service: load config, open and migrate the database, serve until SIGINT/SIGTERM.
package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"github.com/rs/cors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	userservicejwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	userservicemiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
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
}

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "user-service")
	fatal := func(msg string, err error) {
		log.Error(msg, "err", err)
		os.Exit(1)
	}

	cfg, err := loadConfig()
	if err != nil {
		fatal("invalid config", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(cfg.databaseURL, cfg.dbMaxOpen, cfg.dbMaxIdle)
	if err != nil {
		fatal("open database", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		fatal("database handle", err)
	}
	defer sqlDB.Close()

	if cfg.runMigrations {
		if err := database.Migrate(db); err != nil {
			fatal("migrate", err)
		}
		log.Info("migrations applied")
	}

	auth, codec, persistence, err := newAuthServices(db, cfg)
	if err != nil {
		fatal("initialize authentication", err)
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
		Profile: &profilehandler.Handler{Logic: profileLogic},
		Admin: &adminhandler.Handler{Logic: roleLogic},
		Authenticate: userservicemiddleware.AuthenticateLocal(codec),
		Users: persistence.Users,
	}
	handler := getCorsConfig(auth.AllowedOrigin).Handler(router.Setup(&health.Handler{DB: sqlDB}, auth, protected))
	srv := newServer(":"+cfg.port, handler)

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		if err != nil {
			fatal("serve", err)
		}
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), SHUTDOWN_TIMEOUT_SEC*time.Second)
	defer cancel()
	log.Info("shutting down")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error("shutdown", "err", err)
	}
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
// defaults and rejecting a missing DATABASE_URL or malformed values.
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
	}
	if cfg.frontendURL == "" && cfg.appEnv == "local" {
		cfg.frontendURL = "http://localhost:5173"
	}
	if cfg.privateKey == "" && cfg.appEnv == "local" {
		cfg.privateKey = "../.local/secrets/auth/jwt-signing-private.pem"
	}
	if cfg.databaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is not set"))
	}
	if cfg.frontendURL == "" {
		errs = append(errs, errors.New("FRONTEND_BASE_URL is not set"))
	}
	if cfg.privateKey == "" {
		errs = append(errs, errors.New("JWT_PRIVATE_KEY_FILE is not set"))
	}
	return cfg, errors.Join(errs...)
}

func envString(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
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

func newAuthHandler(db *gorm.DB, cfg config) (*authhandler.Handler, error) {
	auth, _, _, err := newAuthServices(db, cfg)
	return auth, err
}

func newAuthServices(db *gorm.DB, cfg config) (*authhandler.Handler, *userservicejwt.ES256Codec, *store.Store, error) {
	frontendURL, err := url.Parse(cfg.frontendURL)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse FRONTEND_BASE_URL: %w", err)
	}
	key, err := userservicejwt.LoadPrivateKeyPEM(cfg.privateKey)
	if err != nil {
		return nil, nil, nil, err
	}
	codec, err := userservicejwt.NewES256Codec(key, keyID(&key.PublicKey),
		userservicemiddleware.TokenIssuer, userservicemiddleware.TokenAudience)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("configure JWT signing: %w", err)
	}
	persistence := store.New(db)
	serviceStore := service.Store{
		Users: persistence.Users, AuthTokens: persistence.AuthTokens,
		Sessions: persistence.Sessions, Domains: persistence.Admin,
	}
	withTransaction := func(ctx context.Context, operation func(service.Store) error) error {
		return persistence.WithTransaction(ctx, func(tx *store.Store) error {
			return operation(service.Store{
				Users: tx.Users, AuthTokens: tx.AuthTokens,
				Sessions: tx.Sessions, Domains: tx.Admin,
			})
		})
	}
	logic, err := service.NewService(service.Dependencies{
		Store: serviceStore, WithTransaction: withTransaction,
		TokenCodec: codec, EmailSender: email.EmptyEmailSender{},
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

func keyID(key *ecdsa.PublicKey) string {
	fingerprint := sha256.Sum256(elliptic.Marshal(key.Curve, key.X, key.Y))
	return hex.EncodeToString(fingerprint[:])
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
