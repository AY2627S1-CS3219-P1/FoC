package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolConfig holds the tunable behaviour of the worker pool.
type PoolConfig struct {
	Concurrency       int
	Lease             time.Duration
	HeartbeatInterval time.Duration
	PollInterval      time.Duration
	LLMRequestTimeout time.Duration
	Policy            Policy
}

// Pool is a bounded, per-process worker pool. It claims eligible jobs, runs one
// bounded assessment through the injected Evaluator port, and finalizes the
// result through the fenced store. It shares the Limiter with the configuration
// test so live assessments never exceed the configured concurrency.
type Pool struct {
	store     *jobStore
	evaluator Evaluator
	limiter   *Limiter
	cfg       PoolConfig
	logger    *slog.Logger
	stats     poolStats
	workers   sync.WaitGroup
}

// NewPool constructs the worker pool.
func NewPool(
	dbPool *pgxpool.Pool,
	evaluator Evaluator,
	limiter *Limiter,
	cfg PoolConfig,
	logger *slog.Logger,
) *Pool {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pool{
		store:     NewJobStore(dbPool, cfg.Policy, nil),
		evaluator: evaluator,
		limiter:   limiter,
		cfg:       cfg,
		logger:    logger,
	}
}

// Start launches the worker goroutines. Workers run until ctx is cancelled;
// callers signal shutdown by cancelling ctx and then wait on Wait. When no
// evaluator is configured (for example, no provider key yet) the pool stays
// idle so queued jobs are never attempted or burned against.
func (p *Pool) Start(ctx context.Context) {
	if p.evaluator == nil {
		p.logger.Info("worker pool disabled: no evaluator configured (no provider key)")
		return
	}
	p.logger.Info("start worker pool",
		"concurrency", p.cfg.Concurrency,
		"lease", p.cfg.Lease,
		"max_attempts", p.cfg.Policy.MaxAttempts,
	)
	for i := 0; i < p.cfg.Concurrency; i++ {
		workerID := fmt.Sprintf("worker-%d", i+1)
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			p.worker(ctx, workerID)
		}()
	}
}

// Wait blocks until all workers have exited.
func (p *Pool) Wait() { p.workers.Wait() }

// release returns an owned job to the queue after a shutdown or a terminal-
// write failure. The shutdown path calls it with an already-cancelled context,
// so the store write must run on a detached context; a failed release is
// logged (the lease machinery remains the safety net).
func (p *Pool) release(ctx context.Context, claimed *ClaimedJob) {
	if _, err := p.store.Release(context.WithoutCancel(ctx), claimed.Job.ID, claimed.ClaimToken); err != nil {
		p.logger.Error("release job", "error", err, "job", claimed.Job.ID.String())
	}
}

// Ingest stores a submitted review as a durable moderation job, deduplicating on
// moderation_request_id. It returns nil when the event is durably handled —
// newly inserted, deduplicated, or quarantined as a conflict or malformed — and
// in all three cases the caller acknowledges the event. Only a transport-level
// error is returned, so the event is retried.
func (p *Pool) Ingest(ctx context.Context, in IngestionInput) (ingestionResult, error) {
	return p.store.Ingest(ctx, in)
}

// QuarantineMalformed records a decoded-but-unprocessable event and acknowledges
// it so the partition can advance. Callers use it when an event cannot become a
// job (for example, a malformed identifier) so it is retried only on transport
// failure, not on a permanent decode problem.
func (p *Pool) QuarantineMalformed(ctx context.Context, in MalformedInput) error {
	return p.store.QuarantineMalformed(ctx, in)
}

// worker is one loop: claim, process, repeat. It never returns until ctx ends.
func (p *Pool) worker(ctx context.Context, workerID string) {
	for {
		if ctx.Err() != nil {
			return
		}

		outcome, claimed, err := p.store.Claim(ctx, workerID, p.cfg.Lease)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			p.logger.Error("claim job", "error", err, "worker", workerID)
			p.idleSleep(ctx, 2*p.cfg.PollInterval)
			continue
		}

		switch outcome {
		case ClaimNone, ClaimNoConfiguration:
			p.idleSleep(ctx, p.cfg.PollInterval)
		case ClaimReady:
			p.processClaimed(ctx, claimed)
		}
	}
}

// processClaimed runs the bounded assessment and finalizes it. The limiter slot
// bounds concurrent evaluations together with the configuration test.
func (p *Pool) processClaimed(ctx context.Context, claimed *ClaimedJob) {
	if err := p.limiter.Acquire(ctx); err != nil {
		return
	}
	defer p.limiter.Release()
	p.stats.inFlight.Add(1)
	defer p.stats.inFlight.Add(-1)

	evalCtx, cancelEval := context.WithTimeout(ctx, p.cfg.LLMRequestTimeout)
	heartbeatStop, claimLost, claimDone := p.heartbeat(ctx, evalCtx, cancelEval, claimed)

	assessment, evalErr := p.evaluator.Evaluate(evalCtx, evaluationInput(claimed))

	// Stop inference and the heartbeat, then let the heartbeat goroutine exit
	// so a pending claim-loss signal is already visible.
	cancelEval()
	heartbeatStop()
	<-claimDone

	if ctx.Err() != nil {
		// Shutting down: return the owned job so a later cycle retries it.
		p.release(ctx, claimed)
		return
	}
	select {
	case <-claimLost:
		return
	default:
	}

	if evalErr == nil {
		p.complete(ctx, claimed, assessment)
		return
	}
	p.finalizeFailed(ctx, claimed, evalErr)
}

// complete validates and persists a valid assessment, or reclassifies an invalid
// one as invalid output.
func (p *Pool) complete(ctx context.Context, claimed *ClaimedJob, assessment Assessment) {
	if err := validateAssessment(assessment); err != nil {
		p.finalizeFailed(ctx, claimed, err)
		return
	}
	if err := p.store.Complete(ctx, *claimed, assessment); err != nil {
		if errors.Is(err, ErrFenced) {
			p.logger.Warn("job completed by another worker", "job", claimed.Job.ID.String())
			return
		}
		p.logger.Error("complete job", "error", err, "job", claimed.Job.ID.String())
		p.release(ctx, claimed)
		return
	}
	p.stats.completed.Add(1)
	p.logger.Info("job completed",
		"job", claimed.Job.ID.String(),
		"outcome", assessment.Outcome,
		"generation", claimed.Job.RunGeneration,
	)
}

// finalizeFailed records the error and schedules a bounded retry or errors the
// job, logging terminal outcomes.
func (p *Pool) finalizeFailed(ctx context.Context, claimed *ClaimedJob, evalErr error) {
	if errors.Is(evalErr, ErrClaimLost) {
		return
	}
	if ctx.Err() != nil {
		p.release(ctx, claimed)
		return
	}
	if err := p.store.FinalizeFailed(ctx, *claimed, evalErr); err != nil {
		p.logger.Error("finalize failed attempt", "error", err, "job", claimed.Job.ID.String())
		p.release(ctx, claimed)
		return
	}

	_, kind, _ := Classify(evalErr)
	switch kind {
	case kindConfiguration:
		p.stats.configPaused.Add(1)
		p.logger.Warn("job paused on configuration error", "job", claimed.Job.ID.String(), "error", evalErr.Error())
	case kindDurable:
		p.stats.errored.Add(1)
		p.logger.Error("job errored terminally", "job", claimed.Job.ID.String(), "error", evalErr.Error())
	}
}

// heartbeat extends the claim lease while an assessment runs. When the claim is
// lost (its fenced UPDATE affected no rows) it cancels the assessment and signals
// through claimLost. stop releases the ticker; done closes when the goroutine
// exits.
func (p *Pool) heartbeat(
	parentCtx, evalCtx context.Context,
	cancelEval context.CancelFunc,
	claimed *ClaimedJob,
) (stop func(), claimLost <-chan struct{}, done <-chan struct{}) {
	claimLostC := make(chan struct{}, 1)
	doneC := make(chan struct{})

	ticker := time.NewTicker(p.cfg.HeartbeatInterval)
	go func() {
		defer ticker.Stop()
		defer close(doneC)
		for {
			select {
			case <-evalCtx.Done():
				return
			case <-parentCtx.Done():
				return
			case <-ticker.C:
				leaseUntil := time.Now().Add(p.cfg.Lease)
				ok, err := p.store.Heartbeat(evalCtx, claimed.Job.ID, claimed.ClaimToken, time.Now(), leaseUntil)
				if err != nil {
					continue
				}
				if !ok {
					cancelEval()
					select {
					case claimLostC <- struct{}{}:
					default:
					}
					return
				}
			}
		}
	}()

	return func() { ticker.Stop() }, claimLostC, doneC
}

// idleSleep waits for the poll interval or ctx cancellation.
func (p *Pool) idleSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// evaluationInput builds the immutable per-run input from the claimed job.
func evaluationInput(claimed *ClaimedJob) EvaluationInput {
	return EvaluationInput{
		ModerationRequestID: claimed.Job.ModerationRequestID.String(),
		ReviewID:            claimed.Job.ReviewID.String(),
		ReviewerID:          claimed.Job.ReviewerID.String(),
		ContentRevision:     claimed.Job.ContentRevision,
		Comment:             claimed.Job.Comment,
		Config:              effectiveConfig(claimed.Config),
	}
}

// configParameters mirrors the jsonb layout persisted by the config domain.
type configParameters struct {
	Temperature         *float64          `json:"temperature"`
	TopP                *float64          `json:"top_p"`
	PresencePenalty     *float64          `json:"presence_penalty"`
	FrequencyPenalty    *float64          `json:"frequency_penalty"`
	MaxCompletionTokens *int64            `json:"max_completion_tokens"`
	MaxTokens           *int64            `json:"max_tokens"`
	Stop                []string          `json:"stop"`
	Seed                *int64            `json:"seed"`
	ReasoningEffort     *string           `json:"reasoning_effort"`
	Advanced            map[string]string `json:"advanced_parameters"`
}

func effectiveConfig(config moderationdb.ModerationConfigVersion) EffectiveConfig {
	var params configParameters
	if err := json.Unmarshal(config.Parameters, &params); err != nil {
		params = configParameters{}
	}

	return EffectiveConfig{
		VersionID:           config.VersionID.String(),
		ModelID:             config.ModelID,
		OutputMode:          OutputMode(config.OutputMode),
		Temperature:         params.Temperature,
		TopP:                params.TopP,
		PresencePenalty:     params.PresencePenalty,
		FrequencyPenalty:    params.FrequencyPenalty,
		MaxCompletionTokens: params.MaxCompletionTokens,
		MaxTokens:           params.MaxTokens,
		Stop:                params.Stop,
		Seed:                params.Seed,
		ReasoningEffort:     derefString(params.ReasoningEffort),
		Advanced:            params.Advanced,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// poolStats aggregates simple exported counters.
type poolStats struct {
	inFlight     atomic.Int64
	completed    atomic.Int64
	errored      atomic.Int64
	configPaused atomic.Int64
}
