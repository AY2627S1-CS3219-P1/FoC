package main

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type appConfig struct {
	// Port serves users; InternalPort must not be published.
	Port           string
	InternalPort   string
	DatabaseURL    string
	UserServiceURL string
	RunMigrations  bool
	// Empty disables Kafka consumers; ingestion and relay then don't run.
	KafkaBrokers []string

	// Worker pool tuning
	WorkerConcurrency    int
	LLMRequestTimeout    time.Duration
	JobLease             time.Duration
	JobHeartbeatInterval time.Duration
	JobMaxAttempts       int
	JobRetryInitialDelay time.Duration
	JobRetryMaxDelay     time.Duration
	JobPollInterval      time.Duration

	// Provider configuration
	LLMBaseURL string
	LLMAPIKey  string
}

// ErrHeartbeatTooLarge is returned when the heartbeat interval is not less than
// the lease duration.
var ErrHeartbeatTooLarge = errors.New("CMS_JOB_HEARTBEAT_INTERVAL must be less than CMS_JOB_LEASE")

func loadConfig(getenv func(string) string) (appConfig, error) {
	c := appConfig{
		Port:                 strings.TrimSpace(getenv("PORT")),
		InternalPort:         strings.TrimSpace(getenv("INTERNAL_PORT")),
		DatabaseURL:          strings.TrimSpace(getenv("DATABASE_URL")),
		RunMigrations:        true,
		WorkerConcurrency:    2,
		LLMRequestTimeout:    120 * time.Second,
		JobLease:             180 * time.Second,
		JobHeartbeatInterval: 10 * time.Second,
		JobMaxAttempts:       5,
		JobRetryInitialDelay: 10 * time.Second,
		JobRetryMaxDelay:     5 * time.Minute,
		JobPollInterval:      1 * time.Second,
		LLMBaseURL:           "https://soclaas-api.comp.nus.edu.sg/v1",
		LLMAPIKey:            strings.TrimSpace(getenv("CMS_LLM_API_KEY")),
	}

	// USER_SERVICE_BASE_URL is the repo-wide convention (compose and the k8s
	// foc-config ConfigMap set it); USER_SERVICE_URL is accepted as a legacy
	// override.
	c.UserServiceURL = strings.TrimSpace(getenv("USER_SERVICE_BASE_URL"))
	if c.UserServiceURL == "" {
		c.UserServiceURL = strings.TrimSpace(getenv("USER_SERVICE_URL"))
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
			return appConfig{}, fmt.Errorf("%s must be between 1 and 65535", name)
		}
	}
	if c.Port == c.InternalPort {
		return appConfig{}, errors.New("PORT and INTERNAL_PORT must differ")
	}

	// Dependencies
	if c.DatabaseURL == "" {
		return appConfig{}, errors.New("DATABASE_URL is required")
	}
	if _, err := pgxpool.ParseConfig(c.DatabaseURL); err != nil {
		return appConfig{}, errors.New("DATABASE_URL must be a database URL")
	}

	if c.UserServiceURL == "" {
		return appConfig{}, errors.New("USER_SERVICE_BASE_URL is required")
	}
	if _, err := url.Parse(c.UserServiceURL); err != nil {
		return appConfig{}, errors.New("USER_SERVICE_BASE_URL must be a valid URL")
	}

	if value := strings.TrimSpace(getenv("RUN_MIGRATIONS")); value != "" {
		parsed, perr := strconv.ParseBool(value)
		if perr != nil {
			return appConfig{}, fmt.Errorf("RUN_MIGRATIONS must be boolean: %w", perr)
		}
		c.RunMigrations = parsed
	}

	if value := strings.TrimSpace(getenv("KAFKA_BROKERS")); value != "" {
		c.KafkaBrokers = strings.Split(value, ",")
	}

	if value := strings.TrimSpace(getenv("CMS_WORKER_CONCURRENCY")); value != "" {
		parsed, perr := strconv.Atoi(value)
		if perr != nil || parsed < 1 {
			return appConfig{}, errors.New("CMS_WORKER_CONCURRENCY must be a positive integer")
		}
		c.WorkerConcurrency = parsed
	}

	if value := strings.TrimSpace(getenv("CMS_LLM_REQUEST_TIMEOUT")); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			c.LLMRequestTimeout = d
		}
	}

	if value := strings.TrimSpace(getenv("CMS_JOB_LEASE")); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			c.JobLease = d
		}
	}

	if value := strings.TrimSpace(getenv("CMS_JOB_HEARTBEAT_INTERVAL")); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			c.JobHeartbeatInterval = d
		}
	}

	if value := strings.TrimSpace(getenv("CMS_JOB_MAX_ATTEMPTS")); value != "" {
		parsed, perr := strconv.Atoi(value)
		if perr != nil || parsed < 1 {
			return appConfig{}, errors.New("CMS_JOB_MAX_ATTEMPTS must be a positive integer")
		}
		c.JobMaxAttempts = parsed
	}

	if value := strings.TrimSpace(getenv("CMS_JOB_RETRY_INITIAL_DELAY")); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			c.JobRetryInitialDelay = d
		}
	}

	if value := strings.TrimSpace(getenv("CMS_JOB_RETRY_MAX_DELAY")); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			c.JobRetryMaxDelay = d
		}
	}

	if value := strings.TrimSpace(getenv("CMS_JOB_POLL_INTERVAL")); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			c.JobPollInterval = d
		}
	}

	if value := strings.TrimSpace(getenv("CMS_LLM_BASE_URL")); value != "" {
		c.LLMBaseURL = value
	}

	// Heartbeat interval must be less than lease
	if c.JobHeartbeatInterval >= c.JobLease {
		return appConfig{}, ErrHeartbeatTooLarge
	}

	return c, nil
}
