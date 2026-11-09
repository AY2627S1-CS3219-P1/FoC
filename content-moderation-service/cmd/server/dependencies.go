package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/openaiadapter"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/router"
	configrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/config"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/health"
	reviewrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/review"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	"github.com/jackc/pgx/v5/pgxpool"
)

// serverComponents bundles the handlers and background workers assembled at startup.
type serverComponents struct {
	rpc     router.RPCServices
	pool    *moderation.Pool
	relay   *outbox.Relay
	service *moderation.Service
}

func newServerComponents(ctx context.Context, pool *pgxpool.Pool, cfg appConfig, logger *slog.Logger) serverComponents {
	policy := moderation.RetrySettingsFromConfig(cfg.JobMaxAttempts, cfg.JobRetryInitialDelay, cfg.JobRetryMaxDelay)
	jobStore := moderation.NewJobService(
		pool,
		policy,
		nil,
		moderation.WithProviderIdentity(cfg.LLMBaseURL),
	)
	configStore := moderation.NewConfigurationStore(pool)

	_ = configStore.EnsureConfigState(ctx)

	settings := moderation.Settings{
		BaseURL:       cfg.LLMBaseURL,
		APIKey:        cfg.LLMAPIKey,
		PolicyVersion: "review-text-v1",
	}

	var client moderation.CompletionClient
	var modelLister moderation.ModelLister
	var limiter *moderation.InferenceLimiter

	if cfg.LLMAPIKey != "" && cfg.LLMBaseURL != "" {
		adapter, err := openaiadapter.New(openaiadapter.Config{
			BaseURL: cfg.LLMBaseURL,
			APIKey:  cfg.LLMAPIKey,
			Timeout: cfg.LLMRequestTimeout,
		})
		if err != nil {
			logger.Warn("failed to create LLM adapter", "error", err)
		} else {
			client = adapter
			modelLister, _ = openaiadapter.NewModelLister(cfg.LLMBaseURL, cfg.LLMAPIKey, 10*time.Second)
			limiter = moderation.NewInferenceLimiter(cfg.WorkerConcurrency)
		}
	}

	service := moderation.NewService(
		configStore, jobStore,
		modelLister,
		client,
		limiter,
		settings,
		time.Now,
	)

	var jobPool *moderation.Pool
	if client != nil {
		jobPool = moderation.NewPool(
			service,
			service,
			limiter,
			moderation.PoolConfig{
				Concurrency:       cfg.WorkerConcurrency,
				Lease:             cfg.JobLease,
				HeartbeatInterval: cfg.JobHeartbeatInterval,
				PollInterval:      cfg.JobPollInterval,
				LLMRequestTimeout: cfg.LLMRequestTimeout,
				RetrySettings:     policy,
			},
			logger,
		)
	}

	var outboxRelay *outbox.Relay
	if len(cfg.KafkaBrokers) > 0 {
		publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: cfg.KafkaBrokers})
		if err == nil {
			outboxRelay = outbox.NewRelay(
				pool,
				publisher,
				outbox.RelayConfig{
					PollInterval: cfg.JobPollInterval,
					Lease:        cfg.JobLease,
				},
				logger,
			)
		} else {
			logger.Warn("failed to create Kafka publisher", "error", err)
		}
	}

	ownerHandler := reviewrpc.NewOwnerHandler(service)
	adminHandler := reviewrpc.NewAdminHandler(service)
	configHandler := configrpc.NewHandler(service)

	return serverComponents{
		rpc: router.RPCServices{
			Health: &healthrpc.Checker{
				DB: pool,
			},
			ReviewModeration:      ownerHandler,
			ReviewModerationAdmin: adminHandler,
			ModerationAdmin:       configHandler,
		},
		pool:    jobPool,
		relay:   outboxRelay,
		service: service,
	}
}
