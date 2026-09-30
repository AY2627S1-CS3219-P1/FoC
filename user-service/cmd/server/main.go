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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/joho/godotenv"
	"github.com/rs/cors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
	SHUTDOWN_TIMEOUT_SEC    = 10
)

type config struct {
	port          string
	databaseURL   string
	dbMaxOpen     int
	dbMaxIdle     int
	runMigrations bool
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

	srv := newServer(":"+cfg.port, getCorsConfig().Handler(newRouter(&health.Handler{DB: sqlDB})))

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
		port:          envString("PORT", "8080"),
		databaseURL:   envString("DATABASE_URL", ""),
		dbMaxOpen:     envPositiveInt("DB_MAX_OPEN", 10, &errs),
		dbMaxIdle:     envPositiveInt("DB_MAX_IDLE", 5, &errs),
		runMigrations: envBool("RUN_MIGRATIONS", true, &errs),
	}
	if cfg.databaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is not set"))
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

func newRouter(healthHandler *health.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)

	r.Mount(userv1connect.NewHealthServiceHandler(healthHandler))
	return r
}

// getCorsConfig allows localhost dev origins.
func getCorsConfig() *cors.Cors {
	return cors.New(cors.Options{
		AllowOriginFunc: func(origin string) bool {
			return strings.HasPrefix(origin, "http://localhost:")
		},
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
