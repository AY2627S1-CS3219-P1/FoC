package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type config struct {
	// Port serves the frontend; InternalPort serves the same routes and is
	// never published.
	Port         string
	InternalPort string
	DatabaseURL  string
	// UserServiceURL is User Service's internal listener: signing keys and the
	// participant reads.
	UserServiceURL string
	// OrderServiceURL is Order's internal listener. Empty fails order report
	// checks closed.
	OrderServiceURL string
	RunMigrations   bool
	// Empty disables the outcome relay; closed outcomes wait in the outbox.
	KafkaBrokers []string

	// Evidence storage. An empty bucket disables image uploads.
	S3Bucket         string
	S3Endpoint       string
	S3PublicEndpoint string
	S3UploadTTL      time.Duration
	S3DownloadTTL    time.Duration
	AWSRegion        string
}

func loadConfig(getenv func(string) string) (config, error) {
	env := func(key string) string { return strings.TrimSpace(getenv(key)) }
	c := config{
		Port:             env("PORT"),
		InternalPort:     env("INTERNAL_PORT"),
		DatabaseURL:      env("DATABASE_URL"),
		UserServiceURL:   env("USER_SERVICE_BASE_URL"),
		OrderServiceURL:  env("ORDER_SERVICE_BASE_URL"),
		RunMigrations:    true,
		S3Bucket:         env("MODERATION_S3_BUCKET"),
		S3Endpoint:       env("MODERATION_S3_ENDPOINT"),
		S3PublicEndpoint: env("MODERATION_S3_PUBLIC_ENDPOINT"),
		AWSRegion:        env("AWS_REGION"),
	}

	// Listeners
	if c.Port == "" {
		c.Port = "8080"
	}
	if c.InternalPort == "" {
		c.InternalPort = "8081"
	}
	for name, value := range map[string]string{"PORT": c.Port, "INTERNAL_PORT": c.InternalPort} {
		port, err := strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return config{}, fmt.Errorf("%s must be between 1 and 65535", name)
		}
	}
	if c.Port == c.InternalPort {
		return config{}, errors.New("PORT and INTERNAL_PORT must differ")
	}

	// Dependencies
	if c.DatabaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}
	if !isOrigin(c.UserServiceURL) {
		return config{}, errors.New("USER_SERVICE_BASE_URL must be an HTTP(S) origin")
	}
	if c.OrderServiceURL != "" && !isOrigin(c.OrderServiceURL) {
		return config{}, errors.New("ORDER_SERVICE_BASE_URL must be an HTTP(S) origin")
	}

	var err error
	if value := env("RUN_MIGRATIONS"); value != "" {
		if c.RunMigrations, err = strconv.ParseBool(value); err != nil {
			return config{}, fmt.Errorf("RUN_MIGRATIONS must be boolean: %w", err)
		}
	}
	if value := env("KAFKA_BROKERS"); value != "" {
		for _, broker := range strings.Split(value, ",") {
			if broker = strings.TrimSpace(broker); broker != "" {
				c.KafkaBrokers = append(c.KafkaBrokers, broker)
			}
		}
	}

	// Evidence storage
	for key, target := range map[string]*time.Duration{
		"MODERATION_S3_UPLOAD_TTL":   &c.S3UploadTTL,
		"MODERATION_S3_DOWNLOAD_TTL": &c.S3DownloadTTL,
	} {
		if value := env(key); value != "" {
			if *target, err = time.ParseDuration(value); err != nil || *target <= 0 {
				return config{}, fmt.Errorf("%s must be a positive duration", key)
			}
		}
	}
	return c, nil
}

// isOrigin accepts an http(s) origin with an optional trailing slash.
func isOrigin(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Host != "" && (parsed.Scheme == "https" || parsed.Scheme == "http") &&
		parsed.User == nil && parsed.RawQuery == "" && parsed.Fragment == "" &&
		(parsed.Path == "" || parsed.Path == "/")
}
