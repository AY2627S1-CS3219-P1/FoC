package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/joho/godotenv"
	"github.com/rs/cors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	healthhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
	SHUTDOWN_TIMEOUT_SEC    = 10
	DEFAULT_DB_MAX_OPEN     = 10
	DEFAULT_DB_MAX_IDLE     = 5
)

// main runs the user service and exits with status 1 if run returns an error.
func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "user-service")
	slog.SetDefault(log)
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run loads optional .env settings, builds the auth service, opens the database,
// and serves HTTP, applying migrations unless RUN_MIGRATIONS is false. SIGINT or
// SIGTERM starts a shutdown with a 10-second timeout. It returns configuration,
// key, database, migration, serving, or shutdown errors; a missing .env file does
// not prevent startup.
func run(log *slog.Logger) error {
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		log.Warn("could not load .env file", "err", err)
	}

	local := os.Getenv("APP_ENV") == "local"
	frontendURL := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	if frontendURL == "" && local {
		frontendURL = "http://localhost:5173"
	}
	frontend, err := url.Parse(frontendURL)
	if err != nil {
		return err
	}

	keyPath := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY_FILE"))
	if keyPath == "" && local {
		keyPath = "../.local/secrets/auth/jwt-signing-private.pem"
	}
	if keyPath == "" {
		return errors.New("JWT_PRIVATE_KEY_FILE is required")
	}
	key, err := jwt.LoadPrivateKeyPEM(keyPath)
	if err != nil {
		return err
	}
	codec, err := jwt.NewES256Codec(key, keyID(&key.PublicKey), authmiddleware.TokenIssuer, authmiddleware.TokenAudience)
	if err != nil {
		return err
	}
	accessTTL, err := getDurationEnv("JWT_ACCESS_TOKEN_TTL", service.AccessTokenLifetime)
	if err != nil {
		return err
	}
	refreshTTL, err := getDurationEnv("JWT_REFRESH_TOKEN_TTL", service.RefreshTokenLifetime)
	if err != nil {
		return err
	}
	origin := frontend.Scheme + "://" + frontend.Host

	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("DATABASE_URL is not set")
	}
	db, err := database.Open(dsn,
		getEnvInt("DB_MAX_OPEN", DEFAULT_DB_MAX_OPEN),
		getEnvInt("DB_MAX_IDLE", DEFAULT_DB_MAX_IDLE),
	)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()

	if getEnvBool("RUN_MIGRATIONS", true) {
		if err := database.Migrate(db); err != nil {
			return err
		}
		log.Info("migrations applied")
	}

	authStore := store.New(db)
	authService, err := service.NewService(service.Dependencies{
		Store: service.Store{
			Users:      authStore.Users,
			AuthTokens: authStore.AuthTokens,
			Sessions:   authStore.Sessions,
			Domains:    authStore.Admin,
		},
		WithTransaction: func(ctx context.Context, operation func(service.Store) error) error {
			return authStore.WithTransaction(ctx, func(txStore *store.Store) error {
				return operation(service.Store{
					Users:      txStore.Users,
					AuthTokens: txStore.AuthTokens,
					Sessions:   txStore.Sessions,
					Domains:    txStore.Admin,
				})
			})
		},
		TokenCodec:  codec,
		EmailSender: email.EmptyEmailSender{},
	}, service.Config{
		FrontendBaseURL: *frontend, LocalDevelopment: local,
		AccessTokenTTL: accessTTL, RefreshTokenTTL: refreshTTL,
	})
	if err != nil {
		return err
	}

	r := router.Setup(
		&healthhandler.Handler{DB: sqlDB},
		&authhandler.Handler{Logic: authService, AllowedOrigin: origin},
	)
	addr := ":" + getPort()
	srv := newServer(addr, getCorsConfig(origin).Handler(r))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), SHUTDOWN_TIMEOUT_SEC*time.Second)
	defer cancel()
	log.Info("shutting down")
	return srv.Shutdown(shutdownCtx)
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

func keyID(key *ecdsa.PublicKey) string {
	fingerprint := sha256.Sum256(elliptic.Marshal(key.Curve, key.X, key.Y))
	return hex.EncodeToString(fingerprint[:])
}

func getDurationEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration < time.Second {
		return 0, errors.New(name + " must be a duration of at least 1s")
	}
	return duration, nil
}

func getPort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return port
	}
	return "8080"
}

// getEnvInt parses the trimmed environment value as a decimal integer, returning
// fallback if it is unset, empty, invalid, or out of range for int.
func getEnvInt(key string, fallback int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return fallback
}

// getEnvBool parses the trimmed environment value with strconv.ParseBool,
// returning fallback if it is unset, empty, or invalid.
func getEnvBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return fallback
}

func getCorsConfig(origin string) *cors.Cors {
	return cors.New(cors.Options{
		AllowedOrigins: []string{origin},
		AllowedMethods: []string{"GET", "POST", "OPTIONS"},
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
		AllowCredentials: true,
	})
}
