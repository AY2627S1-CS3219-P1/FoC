package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/firebase"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/utils/env"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
	AUTH_STARTUP_TIMEOUT    = time.Minute
	AUTH_RETRY_INTERVAL     = 2 * time.Second
)

func main() {
	slog.SetDefault(slog.Default().With("service", "supplier-service"))
	slog.Info("Starting server...")
	if err := godotenv.Load(".env"); err != nil {
		slog.Error("Error loading .env file", "error", err)
	}
	config := env.Get()

	app, err := firebase.InitFirebase(config.FirebaseCredentialsJSON)
	if err != nil {
		slog.Error("Error initializing firebase", "error", err)
		panic(err)
	}

	queries, pgxPool := database.Connect(config.DatabaseURL)
	defer pgxPool.Close()

	// Fetches User Service public keys, so User Service must be reachable.
	authenticator, err := newAuthenticator(context.Background())
	if err != nil {
		slog.Error("Error initializing authentication", "error", err)
		panic(err)
	}

	locationAdmin := location.NewAdminService(location.NewPostgresAdminStore(pgxPool), time.Now)
	r := router.Setup(deps.New(queries, app, pgxPool), authenticator, locationAdmin)
	cors := getCorsConfig().Handler(r)

	port := config.Port

	server := newServer(":"+port, cors)

	slog.Info("Listening on :" + port)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Server failed to start: %v", "error", err)
		panic(err)
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
func newAuthenticator(ctx context.Context) (*middleware.Authenticator, error) {
	deadline := time.Now().Add(AUTH_STARTUP_TIMEOUT)
	for {
		authenticator, err := middleware.NewUserServiceAuthenticator(ctx)
		if err == nil || time.Now().After(deadline) {
			return authenticator, err
		}
		slog.Warn("User Service not ready; retrying", "error", err)
		time.Sleep(AUTH_RETRY_INTERVAL)
	}
}
