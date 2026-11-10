package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/bootstrap"
	"github.com/joho/godotenv"
)

type config struct {
	port             string
	internalPort     string
	databaseURL      string
	dbMaxOpen        int
	dbMaxIdle        int
	runMigrations    bool
	appEnv           string
	frontendURL      string
	privateKey       string
	kafkaBrokers     []string
	accessTokenTTL   time.Duration
	refreshTokenTTL  time.Duration
	communicationURL string
	bootstrap        bootstrap.Config
	s3Bucket         string
	s3Endpoint       string
	s3PublicEndpoint string
	s3UploadTTL      time.Duration
	s3DownloadTTL    time.Duration
	awsRegion        string
}

// loadConfig reads .env (if present) and the environment once, applying
// local defaults and rejecting missing required values or malformed settings.
func loadConfig() (config, error) {
	if err := godotenv.Load(".env"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return config{}, fmt.Errorf("load .env: %w", err)
	}

	var errs []error
	cfg := config{
		port:             envString("PORT", "8080"),
		internalPort:     envString("INTERNAL_PORT", ""),
		databaseURL:      envString("DATABASE_URL", ""),
		dbMaxOpen:        envPositiveInt("DB_MAX_OPEN", 10, &errs),
		dbMaxIdle:        envPositiveInt("DB_MAX_IDLE", 5, &errs),
		runMigrations:    envBool("RUN_MIGRATIONS", true, &errs),
		appEnv:           envString("APP_ENV", ""),
		frontendURL:      envString("FRONTEND_BASE_URL", ""),
		privateKey:       envString("JWT_PRIVATE_KEY_FILE", ""),
		accessTokenTTL:   envDuration("JWT_ACCESS_TOKEN_TTL", 0, &errs),
		refreshTokenTTL:  envDuration("JWT_REFRESH_TOKEN_TTL", 0, &errs),
		communicationURL: envString("COMMUNICATION_SERVICE_BASE_URL", ""),
		bootstrap: bootstrap.Config{
			Email:       envString("BOOTSTRAP_SUPERADMIN_EMAIL", ""),
			DisplayName: envString("BOOTSTRAP_SUPERADMIN_DISPLAY_NAME", ""),
		},
		// Review photo storage is optional: without a bucket the photo RPCs
		// report unavailability and reviews submit without photos.
		s3Bucket:         envString("USER_S3_BUCKET", ""),
		s3Endpoint:       envString("USER_S3_ENDPOINT", ""),
		s3PublicEndpoint: envString("USER_S3_PUBLIC_ENDPOINT", ""),
		// Zero means the adapter's default grant lifetimes.
		s3UploadTTL:   envDuration("USER_S3_UPLOAD_TTL", 0, &errs),
		s3DownloadTTL: envDuration("USER_S3_DOWNLOAD_TTL", 0, &errs),
		awsRegion:     strings.TrimSpace(envString("AWS_REGION", "")),
		kafkaBrokers:  envList("KAFKA_BROKERS", ""),
	}
	if cfg.frontendURL == "" && cfg.appEnv == "local" {
		cfg.frontendURL = "http://localhost:5173"
	}
	if len(cfg.kafkaBrokers) == 0 && cfg.appEnv == "local" {
		cfg.kafkaBrokers = []string{"localhost:9092"}
	}
	if cfg.privateKey == "" && cfg.appEnv == "local" {
		cfg.privateKey = "../.local/secrets/auth/jwt-signing-private.pem"
	}
	// Communication's internal listener on the Compose network.
	if cfg.communicationURL == "" && cfg.appEnv == "local" {
		cfg.communicationURL = "http://communication-service:8081"
	}
	if cfg.databaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is not set"))
	}
	if len(cfg.kafkaBrokers) == 0 {
		errs = append(errs, errors.New("KAFKA_BROKERS must contain at least one broker"))
	}
	if cfg.frontendURL == "" {
		errs = append(errs, errors.New("FRONTEND_BASE_URL is not set"))
	}
	if cfg.privateKey == "" {
		errs = append(errs, errors.New("JWT_PRIVATE_KEY_FILE is not set"))
	}
	if raw := envString("KAFKA_BROKERS", ""); raw != "" {
		for _, broker := range strings.Split(raw, ",") {
			if strings.TrimSpace(broker) == "" {
				errs = append(errs, errors.New("KAFKA_BROKERS must contain comma-separated broker addresses"))
				break
			}
		}
	}
	if u, err := url.Parse(cfg.communicationURL); cfg.communicationURL == "" {
		errs = append(errs, errors.New("COMMUNICATION_SERVICE_BASE_URL is not set"))
	} else if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		errs = append(errs, fmt.Errorf("COMMUNICATION_SERVICE_BASE_URL must be an http(s) URL, got %q",
			cfg.communicationURL))
	}
	if cfg.internalPort == "" {
		errs = append(errs, errors.New("INTERNAL_PORT is required: every service runs the listener split"))
	} else {
		port, err := strconv.Atoi(cfg.internalPort)
		if err != nil || port < 1 || port > 65535 {
			errs = append(errs, errors.New("INTERNAL_PORT must be between 1 and 65535"))
		} else if cfg.internalPort == cfg.port {
			errs = append(errs, errors.New("INTERNAL_PORT must differ from PORT"))
		}
	}
	return cfg, errors.Join(errs...)
}

func envString(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envList(key, fallback string) []string {
	value := envString(key, fallback)
	items := strings.Split(value, ",")
	result := make([]string, 0, len(items))
	for _, item := range items {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
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

func envDuration(key string, fallback time.Duration, errs *[]error) time.Duration {
	v := envString(key, "")
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a valid duration, got %q", key, v))
		return fallback
	}
	return d
}
