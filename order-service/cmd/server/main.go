package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	transport "github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
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
	if handled, err := transport.ExecuteCommand(ctx, os.Args[1:], os.Getenv, orderv1connect.HealthServiceCheckProcedure, database.Migrate); handled {
		return err
	}
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

	services := newRPCServices(pool)

	var public, internal http.Handler
	if config.Transport.Mode == transport.Mesh {
		public, internal = router.SetupMesh(services, authenticator.Authenticate)
	} else {
		public = router.Setup(services, authenticator.Authenticate)
	}
	cors := getCorsConfig()
	public = cors.Handler(public)
	if internal != nil {
		internal = cors.Handler(internal)
	}
	servers := config.Transport.Servers(public, internal)
	for _, server := range servers {
		server.IdleTimeout = time.Minute
	}
	return transport.Run(ctx, transport.ShutdownTimeout, servers...)
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
	server := transport.New(addr, handler)
	server.IdleTimeout = time.Minute
	return server
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
