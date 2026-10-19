package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	Transport      server.Config
	Port           string
	DatabaseURL    string
	UserServiceURL string
	RunMigrations  bool
}

func loadConfig(getenv func(string) string) (config, error) {
	transport, err := server.LoadConfig(getenv)
	if err != nil {
		return config{}, err
	}
	c := config{
		Transport:      transport,
		Port:           transport.Port,
		DatabaseURL:    strings.TrimSpace(getenv("DATABASE_URL")),
		UserServiceURL: strings.TrimSpace(getenv("USER_SERVICE_BASE_URL")),
		RunMigrations:  true,
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
	if parsed.Scheme != "https" && getenv("APP_ENV") != "local" && c.Transport.Mode != server.Mesh {
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
