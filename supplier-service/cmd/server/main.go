package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	transport "github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/utils/env"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

const (
	AUTH_STARTUP_TIMEOUT = time.Minute
	AUTH_RETRY_INTERVAL  = 2 * time.Second
)

func main() {
	slog.SetDefault(slog.Default().With("service", "supplier-service"))
	slog.Info("Starting server...")
	if err := godotenv.Load(".env"); err != nil {
		slog.Error("Error loading .env file", "error", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("Supplier service stopped", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	transportConfig, err := transport.LoadConfig(os.Getenv)
	if err != nil {
		return err
	}
	config := env.Get()

	pgxPool := database.Connect(config.DatabaseURL)
	defer pgxPool.Close()

	// Fetches User Service public keys, so User Service must be reachable.
	authenticator, err := newAuthenticator(ctx)
	if err != nil {
		return fmt.Errorf("initialize authentication: %w", err)
	}

	services, err := newRPCServices(pgxPool, time.Now)
	if err != nil {
		return err
	}
	r := router.Setup(deps.New(pgxPool), authenticator, services)
	cors := getCorsConfig().Handler(r)

	servers, err := transportConfig.Servers(transport.Handlers{
		Public:   cors,
		Internal: router.Internal(),
	})
	if err != nil {
		return err
	}
	return transport.Run(ctx, transport.ShutdownTimeout, servers...)
}

// getCorsConfig allows credentialed cross-origin requests from HTTP localhost
// origins with a port and HTTPS yihao03*.expo.app origins. It allows Connect and
// gRPC-Web request headers and exposes gRPC response status headers.
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

// newAuthenticator retries while User Service starts, since its public keys
// are fetched once at startup.
func newAuthenticator(ctx context.Context) (*httpauth.Authenticator, error) {
	return retryAuthenticator(ctx, AUTH_RETRY_INTERVAL, httpauth.NewUserServiceAuthenticator)
}

func retryAuthenticator(
	ctx context.Context,
	interval time.Duration,
	build func(context.Context) (*httpauth.Authenticator, error),
) (*httpauth.Authenticator, error) {
	ctx, cancel := context.WithTimeout(ctx, AUTH_STARTUP_TIMEOUT)
	defer cancel()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		authenticator, err := build(ctx)
		if err == nil {
			return authenticator, err
		}
		slog.Warn("User Service not ready; retrying", "error", err)
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
