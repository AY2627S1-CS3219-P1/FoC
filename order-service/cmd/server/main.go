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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/images"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

func main() {
	slog.SetDefault(slog.Default().With("service", "order-service"))

	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Load .env", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx); err != nil {
		slog.Error("Order service stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	config, err := loadConfig(os.Getenv)
	if err != nil {
		return err
	}

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

	repository := order.NewPostgresRepository(pool)
	var storage images.ObjectStorage
	if config.S3Bucket != "" {
		storage, err = images.NewS3(startupCtx, images.S3Config{
			Bucket:         config.S3Bucket,
			Region:         config.AWSRegion,
			Endpoint:       config.S3Endpoint,
			PublicEndpoint: config.S3PublicEndpoint,
		})
		if err != nil {
			return fmt.Errorf("initialize image storage: %w", err)
		}
	}
	itemImages := order.NewItemImages(repository, storage)
	creator, recoverCreations, err := newCreator(startupCtx, repository, config, itemImages)
	if err != nil {
		return fmt.Errorf("initialize creation: %w", err)
	}
	assigner, recoverAssignments, err := newAssigner(startupCtx, repository, config)
	if err != nil {
		return fmt.Errorf("initialize assignment: %w", err)
	}
	var publisher *kafka.Publisher
	if config.KafkaBrokers != "" {
		publisher, err = kafka.NewPublisher(kafka.PublisherConfig{Brokers: strings.Split(config.KafkaBrokers, ",")})
		if err != nil {
			return err
		}
		defer func() { _ = publisher.Close() }()
	}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	if recoverCreations {
		workers.Go(func() { creator.RecoverCreations(workerCtx) })
	}
	if recoverAssignments {
		workers.Go(func() { assigner.RecoverAssignments(workerCtx) })
	}
	if publisher != nil {
		relay := outbox.New(outbox.NewPostgres(pool), publisher)
		workers.Go(func() { relay.Run(workerCtx) })
	}
	if storage != nil {
		workers.Go(func() { cleanupImages(workerCtx, itemImages) })
	}
	defer func() { stopWorkers(); workers.Wait() }()

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

	services := newRPCServices(pool, creator, itemImages, assigner)

	routes := router.Setup(services, authenticator.Authenticate)
	handler := getCorsConfig().Handler(routes)

	server := newServer(":"+config.Port, handler)
	server.BaseContext = func(net.Listener) context.Context { return ctx }

	return serve(ctx, server)
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

func serve(ctx context.Context, server *http.Server) error {
	stopped := make(chan error, 1)
	go func() { stopped <- server.ListenAndServe() }()
	slog.InfoContext(ctx, "Listening", "address", server.Addr)

	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err

	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown server: %w", err)
		}

		if err := <-stopped; !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	}
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

func cleanupImages(ctx context.Context, images *order.ItemImages) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := images.CleanupOnce(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Image cleanup deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
