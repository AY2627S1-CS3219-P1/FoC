package main

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/firebase"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
)

func main() {
	slog.SetDefault(slog.Default().With("service", "user-service"))
	slog.Info("Starting server...")
	if err := godotenv.Load(".env"); err != nil {
		slog.Error("Error loading .env file", "error", err)
	}

	app, err := firebase.InitFirebase()
	if err != nil {
		slog.Error("Error initializing firebase", "error", err)
		panic(err)
	}

	queries, pgxPool := database.Connect()
	defer pgxPool.Close()

	r := router.Setup(deps.New(queries, app, pgxPool))
	cors := getCorsConfig().Handler(r)

	port := getPort()

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           cors,
		ReadHeaderTimeout: READ_HEADER_TIMEOUT_SEC * time.Second,
	}

	slog.Info("Listening on :" + port)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Server failed to start: %v", "error", err)
		panic(err)
	}
}

func getPort() string {
	if port := strings.TrimSpace(os.Getenv("PORT")); port != "" {
		return port
	}
	return "8080"
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
