package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	transport "github.com/AY2627S1-CS3219-P1/FoC/pkg/server"
	"github.com/jackc/pgx/v5/pgxpool"
)

type config struct {
	Transport      transport.Config
	DatabaseURL    string
	UserServiceURL string
	RunMigrations  bool
	// Empty disables the consumers; settlement then waits until it is set.
	KafkaBrokers []string
}

func loadConfig(getenv func(string) string) (config, error) {
	serverConfig, err := transport.LoadConfig(getenv)
	if err != nil {
		return config{}, err
	}
	c := config{
		Transport:      serverConfig,
		DatabaseURL:    strings.TrimSpace(getenv("DATABASE_URL")),
		UserServiceURL: strings.TrimSpace(getenv("USER_SERVICE_BASE_URL")),
		RunMigrations:  true,
	}

	if c.Transport.InternalPort == "" {
		return config{}, errors.New("INTERNAL_PORT is required for Credit reservation routes")
	}

	// Dependencies
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
