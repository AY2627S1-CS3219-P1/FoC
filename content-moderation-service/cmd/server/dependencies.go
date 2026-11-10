package main

import (
	"context"
	"fmt"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"log/slog"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/openaiadapter"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/router"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/health"
	reviewrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/review"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	"github.com/jackc/pgx/v5/pgxpool"
)

type serverComponents struct {
	rpc     router.RPCServices
	pool    *moderationjob.Pool
	relay   *outbox.Relay
	service *moderationjob.Service
}

func newServerComponents(
	ctx context.Context,
	pool *pgxpool.Pool,
	cfg appConfig,
	logger *slog.Logger,
) (serverComponents, error) {
	policy := moderationjob.RetrySettingsFromConfig(cfg.JobMaxAttempts, cfg.JobRetryInitialDelay, cfg.JobRetryMaxDelay)
	jobStore := moderationjob.NewService(pool, policy, nil, cfg.LLMModelID)

	var client moderation.CompletionClient
	var limiter *moderationjob.InferenceLimiter

	if cfg.LLMAPIKey != "" && cfg.LLMBaseURL != "" {
		adapter, err := openaiadapter.New(openaiadapter.Config{
			BaseURL: cfg.LLMBaseURL,
			APIKey:  cfg.LLMAPIKey,
			Timeout: cfg.LLMRequestTimeout,
		})
		if err != nil {
			return serverComponents{}, fmt.Errorf("create moderation provider: %w", err)
		} else {
			client = adapter
			limiter = moderationjob.NewInferenceLimiter(cfg.WorkerConcurrency)
		}
	}

	service := moderation.NewService(client, cfg.LLMModelID)

	var jobPool *moderationjob.Pool
	if client != nil {
		jobPool = moderationjob.NewPool(
			jobStore,
			service,
			limiter,
			moderationjob.PoolConfig{
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

	ownerHandler := reviewrpc.NewOwnerHandler(jobStore)
	adminHandler := reviewrpc.NewAdminHandler(jobStore)

	return serverComponents{
		rpc: router.RPCServices{
			Health: &healthrpc.Checker{
				DB: pool,
			},
			ReviewModeration:      ownerHandler,
			ReviewModerationAdmin: adminHandler,
		},
		pool:    jobPool,
		relay:   outboxRelay,
		service: jobStore,
	}, nil
}
