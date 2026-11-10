package main

import (
	"context"
	"database/sql"
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

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/rs/cors"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
)

func main() {
	slog.SetDefault(slog.Default().With("service", "moderation-service"))

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Load .env", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("Moderation service stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	c, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}

	// Database
	startupCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()

	db, err := sql.Open("pgx", c.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close() //nolint:errcheck
	if err := db.PingContext(startupCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}
	if c.RunMigrations {
		if err := database.Migrate(startupCtx, db); err != nil {
			return err
		}
	}

	services, err := newServices(startupCtx, db, c)
	if err != nil {
		return err
	}

	// Background work: the outcome relay and orphan evidence cleanup.
	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stopWorkers(); workers.Wait() }()

	if len(c.KafkaBrokers) > 0 {
		publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: c.KafkaBrokers})
		if err != nil {
			return err
		}
		defer publisher.Close() //nolint:errcheck
		workers.Go(func() { outbox.NewRelay(db, publisher).Run(workerCtx) })
	} else {
		slog.WarnContext(ctx, "KAFKA_BROKERS is not set; closed outcomes wait in the outbox")
	}
	if services.storage != nil {
		workers.Go(func() { cleanEvidence(workerCtx, services) })
	}

	// RPC listeners
	authenticator, err := newAuthenticator(
		startupCtx,
		2*time.Second,
		func(ctx context.Context) (*httpauth.Authenticator, error) {
			return httpauth.NewAuthenticator(ctx, httpauth.AuthConfig{
				UserServiceURL: c.UserServiceURL,
				Issuer:         httpauth.TokenIssuer,
				Audience:       httpauth.TokenAudience,
			})
		},
	)
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}

	public := newServer(
		":"+c.Port,
		getCorsConfig().Handler(router.Public(services.rpc, authenticator.Authenticate)),
	)
	internal := newServer(":"+c.InternalPort, router.Internal(services.rpc, authenticator.Authenticate))
	for _, server := range []*http.Server{public, internal} {
		server.BaseContext = func(net.Listener) context.Context { return ctx }
	}

	return serve(ctx, public, internal)
}

// cleanEvidence reaps unattached evidence images every minute; each step is
// retryable and idempotent.
func cleanEvidence(ctx context.Context, s services) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.evidence.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Evidence cleanup deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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

// newAuthenticator retries until User Service publishes its signing keys,
// bounded by the caller's startup context.
func newAuthenticator(
	ctx context.Context,
	interval time.Duration,
	build func(context.Context) (*httpauth.Authenticator, error),
) (*httpauth.Authenticator, error) {
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
