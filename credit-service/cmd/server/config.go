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
	// Port serves users; InternalPort serves Order and must not be published.
	Port           string
	InternalPort   string
	DatabaseURL    string
	UserServiceURL string
	RunMigrations  bool
	// Empty disables the consumers; settlement then waits until it is set.
	KafkaBrokers []string
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		Port:           strings.TrimSpace(getenv("PORT")),
		InternalPort:   strings.TrimSpace(getenv("INTERNAL_PORT")),
		DatabaseURL:    strings.TrimSpace(getenv("DATABASE_URL")),
		UserServiceURL: strings.TrimSpace(getenv("USER_SERVICE_BASE_URL")),
		RunMigrations:  true,
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
	if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return config{}, errors.New("DATABASE_URL must be a database URL")
	}

	parsed, err := url.Parse(c.UserServiceURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
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

	if value := strings.TrimSpace(getenv("KAFKA_BROKERS")); value != "" {
		c.KafkaBrokers = strings.Split(value, ",")
	}

	return c, nil
}
