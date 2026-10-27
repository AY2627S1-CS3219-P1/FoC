package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

func main() {
	slog.SetDefault(slog.Default().With("service", "credit-service"))

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Load .env", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("Credit service stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	config, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}

	// Database
	startupCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	pool, err := pgxpool.New(startupCtx, config.DatabaseURL)
	if err != nil {
		return fmt.Errorf("construct database pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(startupCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	if config.RunMigrations {
		if err := database.Migrate(startupCtx, config.DatabaseURL); err != nil {
			return err
		}
	}

	services := newServices(pool)

	// Kafka consumers. Without them Order's outcomes wait in Kafka, and
	// reservations stay held until Credit consumes them.
	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stopWorkers(); workers.Wait() }()

	if len(config.KafkaBrokers) > 0 {
		workers.Go(func() { consume(workerCtx, config.KafkaBrokers, services.outcomes, services.users) })
	} else {
		slog.WarnContext(ctx, "KAFKA_BROKERS is not set; order outcomes and new users are not consumed")
	}

	// RPC listeners
	authenticator, err := newAuthenticator(startupCtx, 2*time.Second, func(ctx context.Context) (*httpauth.Authenticator, error) {
		return httpauth.NewAuthenticator(ctx, httpauth.AuthConfig{
			UserServiceURL: config.UserServiceURL,
			Issuer:         httpauth.TokenIssuer,
			Audience:       httpauth.TokenAudience,
		})
	})
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}

	public := newServer(":"+config.Port, getCorsConfig().Handler(router.Public(services.rpc, authenticator.Authenticate)))
	internal := newServer(":"+config.InternalPort, router.Internal(services.rpc))
	for _, server := range []*http.Server{public, internal} {
		server.BaseContext = func(net.Listener) context.Context { return ctx }
	}

	return serve(ctx, public, internal)
}

func getCorsConfig() *cors.Cors {
	return cors.New(cors.Options{
		AllowOriginFunc: func(origin string) bool {
			parsed, err := url.Parse(origin)
			return err == nil && parsed.Scheme == "http" &&
				(parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1") &&
				parsed.User == nil && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == ""
		},
		AllowCredentials: true,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodOptions},
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

func newServer(addr string, handler http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       time.Minute,
		Protocols:         protocols,
	}
}

// serve runs every server until ctx ends or one of them stops, then shuts
// them all down.
func serve(ctx context.Context, servers ...*http.Server) error {
	stopped := make(chan error, len(servers))
	for _, server := range servers {
		go func() { stopped <- server.ListenAndServe() }()
		slog.InfoContext(ctx, "Listening", "address", server.Addr)
	}

	running := len(servers)
	var errs []error
	select {
	case err := <-stopped:
		running--
		errs = append(errs, err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

// newAuthenticator bounds both network attempts and waits by the caller's
// startup context; callbacks are construction attempts, never transactions.
func newAuthenticator(ctx context.Context, interval time.Duration, build func(context.Context) (*httpauth.Authenticator, error)) (*httpauth.Authenticator, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		authenticator, err := build(ctx)
		if err == nil {
			return authenticator, nil
		}

		slog.WarnContext(ctx, "User Service authentication keys unavailable; retrying", "error", err)

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
