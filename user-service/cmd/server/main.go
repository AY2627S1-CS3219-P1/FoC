package main

import (
	"context"
	"errors"
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
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
	SHUTDOWN_TIMEOUT_SEC    = 10
	DEFAULT_DB_MAX_OPEN     = 10
	DEFAULT_DB_MAX_IDLE     = 5
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("service", "user-service")
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	if err := godotenv.Load(".env"); err != nil {
		log.Warn("no .env file loaded", "err", err)
	}

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

	addr := ":" + getPort()
	srv := &http.Server{
		Addr:              addr,
		Handler:           getCorsConfig().Handler(newRouter(db)),
		ReadHeaderTimeout: READ_HEADER_TIMEOUT_SEC * time.Second,
	}

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

// newRouter wires routes against the GORM handle. Add user routes here.
func newRouter(db *gorm.DB) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
			sqlDB, err := db.DB()
			if err == nil {
				err = sqlDB.PingContext(r.Context())
			}
			if err != nil {
				http.Error(w, "database unavailable", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		})
	})
	return r
}

func getPort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return port
	}
	return "8080"
}

func getEnvInt(key string, fallback int) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return fallback
}

func getCorsConfig() *cors.Cors {
	return cors.New(cors.Options{
		AllowOriginFunc: func(origin string) bool {
			// Allow localhost for development (any local port)
			if strings.HasPrefix(origin, "http://localhost:") {
				return true
			}
			// Allow Expo dev URLs matching pattern
			// This will match: https://yihao03-<project>-<hash>.expo.app
			if len(origin) > 20 && origin[:15] == "https://yihao03" && origin[len(origin)-9:] == ".expo.app" {
				return true
			}
			return false
		},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
	})
}
