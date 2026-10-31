package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	Port               string
	DatabaseURL        string
	UserServiceURL     string
	RunMigrations      bool
	SupplierServiceURL string
	CreditServiceURL   string
	CreditTokenFile    string
	KafkaBrokers       string
	S3Bucket           string
	S3Endpoint         string
	S3PublicEndpoint   string
	AWSRegion          string
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		Port:               strings.TrimSpace(getenv("PORT")),
		DatabaseURL:        strings.TrimSpace(getenv("DATABASE_URL")),
		UserServiceURL:     strings.TrimSpace(getenv("USER_SERVICE_BASE_URL")),
		RunMigrations:      true,
		SupplierServiceURL: strings.TrimSpace(getenv("SUPPLIER_SERVICE_BASE_URL")),
		CreditServiceURL:   strings.TrimSpace(getenv("CREDIT_SERVICE_BASE_URL")),
		CreditTokenFile:    strings.TrimSpace(getenv("CREDIT_WORKLOAD_TOKEN_FILE")),
		KafkaBrokers:       strings.TrimSpace(getenv("KAFKA_BROKERS")),
		S3Bucket:           strings.TrimSpace(getenv("ORDER_S3_BUCKET")),
		S3Endpoint:         strings.TrimSpace(getenv("ORDER_S3_ENDPOINT")),
		S3PublicEndpoint:   strings.TrimSpace(getenv("ORDER_S3_PUBLIC_ENDPOINT")),
		AWSRegion:          strings.TrimSpace(getenv("AWS_REGION")),
	}

	if c.Port == "" {
		c.Port = "8080"
	}
	port, err := strconv.Atoi(c.Port)
	if err != nil || port < 1 || port > 65535 {
		return config{}, errors.New("PORT must be between 1 and 65535")
	}

	if c.DatabaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}
	if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return config{}, errors.New("DATABASE_URL must be a database URL")
	}

	parsed, err := url.Parse(c.UserServiceURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return config{}, errors.New("USER_SERVICE_BASE_URL must be an HTTP(S) origin")
	}
	if parsed.Scheme != "https" && getenv("APP_ENV") != "local" {
		return config{}, errors.New("USER_SERVICE_BASE_URL must use HTTPS outside local mode")
	}

	if value := strings.TrimSpace(getenv("RUN_MIGRATIONS")); value != "" {
		c.RunMigrations, err = strconv.ParseBool(value)
		if err != nil {
			return config{}, fmt.Errorf("RUN_MIGRATIONS must be boolean: %w", err)
		}
	}

	for name, value := range map[string]string{
		"SUPPLIER_SERVICE_BASE_URL": c.SupplierServiceURL,
		"CREDIT_SERVICE_BASE_URL":   c.CreditServiceURL,
		"ORDER_S3_ENDPOINT":         c.S3Endpoint,
		"ORDER_S3_PUBLIC_ENDPOINT":  c.S3PublicEndpoint,
	} {
		if value == "" {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
			parsed.User != nil ||
			parsed.RawQuery != "" ||
			parsed.Fragment != "" ||
			(parsed.Path != "" && parsed.Path != "/") ||
			(parsed.Scheme != "https" && getenv("APP_ENV") != "local") {
			return config{}, fmt.Errorf("%s must be an HTTP(S) origin, HTTPS outside local mode", name)
		}
	}
	if (c.CreditServiceURL == "") != (c.CreditTokenFile == "") {
		return config{}, errors.New(
			"CREDIT_SERVICE_BASE_URL and CREDIT_WORKLOAD_TOKEN_FILE must be configured together",
		)
	}
	if c.S3Bucket == "" && (c.S3Endpoint != "" || c.S3PublicEndpoint != "") {
		return config{}, errors.New("ORDER_S3_BUCKET is required with S3 endpoints")
	}

	return c, nil
}
