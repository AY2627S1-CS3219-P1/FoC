package main

import (
	"context"
	"log/slog"
	"time"

	cfgpkg "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/evaladapter"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/openaiadapter"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/review"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/router"
	configrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/config"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/health"
	reviewrpc "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/rpc/review"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	"github.com/jackc/pgx/v5/pgxpool"
)

// services are CMS's adapters over one set of domain services.
type services struct {
	rpc      router.RPCServices
	pool     *jobs.Pool
	relay    *outbox.Relay
	jobStore *jobs.JobStore
}

func newServices(ctx context.Context, pool *pgxpool.Pool, cfg appConfig, logger *slog.Logger) services {
	// Database-backed stores
	policy := jobs.PolicyFromConfig(cfg.JobMaxAttempts, cfg.JobRetryInitialDelay, cfg.JobRetryMaxDelay)
	jobStore := jobs.NewJobStore(pool, policy, nil, jobs.WithProviderIdentity(cfg.LLMBaseURL))
	reviewStore := review.NewStore(pool)
	configStore := cfgpkg.NewStore(pool)

	// Ensure the config state singleton exists
	_ = configStore.EnsureConfigState(ctx)

	// Configuration service
	settings := cfgpkg.Settings{
		BaseURL:       cfg.LLMBaseURL,
		APIKey:        cfg.LLMAPIKey,
		PolicyVersion: "review-text-v1",
	}

	var evaluator jobs.Evaluator
	var modelLister cfgpkg.ModelLister
	var limiter *jobs.Limiter

	if cfg.LLMAPIKey != "" && cfg.LLMBaseURL != "" {
		// Create the evaluator chain: openaiadapter -> llmpolicy -> evaladapter
		adapter, err := openaiadapter.New(openaiadapter.Config{
			BaseURL: cfg.LLMBaseURL,
			APIKey:  cfg.LLMAPIKey,
			Timeout: cfg.LLMRequestTimeout,
		})
		if err != nil {
			logger.Warn("failed to create LLM adapter", "error", err)
		} else {
			llmEvaluator := llmpolicy.NewEvaluator(adapter)
			evaluator = evaladapter.Wrap(llmEvaluator)
			modelLister, _ = cfgpkg.NewModelLister(cfg.LLMBaseURL, cfg.LLMAPIKey, 10*time.Second)
			limiter = jobs.NewLimiter(cfg.WorkerConcurrency)
		}
	}

	configService := cfgpkg.NewService(
		configStore,
		modelLister,
		evaluator,
		limiter,
		settings,
		time.Now,
	)

	// Job pool - may be nil if no evaluator configured
	var jobPool *jobs.Pool
	if evaluator != nil {
		jobPool = jobs.NewPool(
			jobStore,
			evaluator,
			limiter,
			jobs.PoolConfig{
				Concurrency:       cfg.WorkerConcurrency,
				Lease:             cfg.JobLease,
				HeartbeatInterval: cfg.JobHeartbeatInterval,
				PollInterval:      cfg.JobPollInterval,
				LLMRequestTimeout: cfg.LLMRequestTimeout,
				Policy:            policy,
			},
			logger,
		)
	}

	// Outbox relay - may be nil if no Kafka configured
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

	// RPC services
	ownerHandler := reviewrpc.NewOwnerHandler(reviewStore)
	adminHandler := reviewrpc.NewAdminHandler(reviewStore)
	configHandler := configrpc.NewHandler(configService)

	return services{
		rpc: router.RPCServices{
			Health: &healthrpc.Checker{
				DB: pool,
			},
			ModerationHealth:      &healthrpc.ModerationChecker{DB: pool},
			ReviewModeration:      ownerHandler,
			ReviewModerationAdmin: adminHandler,
			ModerationAdmin:       configHandler,
		},
		pool:     jobPool,
		relay:    outboxRelay,
		jobStore: jobStore,
	}
}
