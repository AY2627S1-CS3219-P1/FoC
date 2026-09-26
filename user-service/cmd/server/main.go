package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/joho/godotenv"
	"github.com/rs/cors"
)

const (
	READ_HEADER_TIMEOUT_SEC = 5 //nolint:gosec
)

func main() {
	slog.SetDefault(slog.Default().With("service", "user-service"))
	slog.Info("Starting server...")
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("Could not load .env file", "error", err)
	}

	local := os.Getenv("APP_ENV") == "local"
	frontendURL := strings.TrimSpace(os.Getenv("FRONTEND_BASE_URL"))
	if frontendURL == "" && local {
		frontendURL = "http://localhost:5173"
	}

	keyPath := strings.TrimSpace(os.Getenv("JWT_PRIVATE_KEY_FILE"))
	if keyPath == "" && local {
		keyPath = "../.local/secrets/auth/jwt-signing-private.pem"
	}
	var key *ecdsa.PrivateKey
	var err error
	if keyPath != "" {
		key, err = auth.LoadPrivateKeyPEM(keyPath)
	} else {
		err = errors.New("JWT_PRIVATE_KEY_FILE is required")
	}
	if err != nil {
		panic(err)
	}
	codec, err := auth.NewES256Codec(key, keyID(&key.PublicKey), authmiddleware.TokenIssuer, authmiddleware.TokenAudience)
	if err != nil {
		panic(err)
	}
	accessTTL, err := getDurationEnv("JWT_ACCESS_TOKEN_TTL", auth.AccessTokenLifetime)
	if err != nil {
		panic(err)
	}
	refreshTTL, err := getDurationEnv("JWT_REFRESH_TOKEN_TTL", auth.RefreshTokenLifetime)
	if err != nil {
		panic(err)
	}
	service, err := auth.NewService(auth.Dependencies{TokenCodec: codec, EmailSender: email.EmptyEmailSender{}}, auth.Config{
		FrontendBaseURL: frontendURL, LocalDevelopment: local,
		AccessTokenTTL: accessTTL, RefreshTokenTTL: refreshTTL})
	if err != nil {
		panic(err)
	}
	frontend, _ := url.Parse(frontendURL) // validated by NewService
	origin := frontend.Scheme + "://" + frontend.Host
	r := router.Setup(&deps.Env{}, &authhandler.Handler{Logic: service, AllowedOrigin: origin})
	corsHandler := getCorsConfig(origin).Handler(r)

	port := getPort()

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           corsHandler,
		ReadHeaderTimeout: READ_HEADER_TIMEOUT_SEC * time.Second,
	}

	slog.Info("Listening on :" + port)
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Server failed to start", "error", err)
		panic(err)
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

func getCorsConfig(origin string) *cors.Cors {
	return cors.New(cors.Options{
		AllowedOrigins:   []string{origin},
		AllowCredentials: true,
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Authorization", "Content-Type"},
	})
}
