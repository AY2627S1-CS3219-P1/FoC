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
	Port           string
	DatabaseURL    string
	UserServiceURL string
	RunMigrations  bool
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		Port:           strings.TrimSpace(getenv("PORT")),
		DatabaseURL:    strings.TrimSpace(getenv("DATABASE_URL")),
		UserServiceURL: strings.TrimSpace(getenv("USER_SERVICE_BASE_URL")),
		RunMigrations:  true,
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

	return c, nil
}
