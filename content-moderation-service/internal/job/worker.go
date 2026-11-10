package job

import (
	"context"
	"errors"
	"fmt"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type PoolConfig struct {
	Concurrency        int
	Lease              time.Duration
	HeartbeatInterval  time.Duration
	PollInterval       time.Duration
	LLMRequestTimeout  time.Duration
	RetrySettings      RetrySettings
	CleanupTimeout     time.Duration
	PersistenceTimeout time.Duration
}

type Pool struct {
	store   jobOperations
	service Assessor
	limiter *InferenceLimiter
	cfg     PoolConfig
	logger  *slog.Logger
	stats   poolStats
	workers sync.WaitGroup
	stop    context.CancelFunc
}

func NewPool(
	repository jobOperations,
	service Assessor,
	limiter *InferenceLimiter,
	cfg PoolConfig,
	logger *slog.Logger,
) *Pool {
	if logger == nil {
		logger = slog.Default()
	}
	return &Pool{
		store:   repository,
		service: service,
		limiter: limiter,
		cfg:     cfg,
		logger:  logger,
	}
}

// Start runs workers until cancellation. Missing credentials leave the queue idle.
func (p *Pool) Start(ctx context.Context) {
	ctx, p.stop = context.WithCancel(ctx)
	if p.service == nil {
		p.logger.Info("worker pool disabled: no provider configured (no provider key)")
		return
	}
	p.logger.Info("start worker pool",
		"concurrency", p.cfg.Concurrency,
		"lease", p.cfg.Lease,
		"max_attempts", p.cfg.RetrySettings.MaxAttempts,
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

func (p *Pool) Wait() { p.workers.Wait() }

// Cleanup must survive caller cancellation; lease expiry covers failed release.
func (p *Pool) release(ctx context.Context, claimed *ClaimedJob) {
	timeout := p.cfg.CleanupTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if _, err := p.store.Release(cleanupCtx, claimed.Job.ID, claimed.ClaimToken); err != nil {
		p.logger.Error("release job", "error", err, "job", claimed.Job.ID.String())
	}
}

func (p *Pool) persistenceContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := p.cfg.PersistenceTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if p.cfg.Lease > 0 && timeout > p.cfg.Lease/2 {
		timeout = p.cfg.Lease / 2
	}
	return context.WithTimeout(ctx, timeout)
}

func (p *Pool) worker(ctx context.Context, workerID string) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := p.limiter.Acquire(ctx); err != nil {
			return
		}

		claimCtx, cancelClaim := p.persistenceContext(ctx)
		outcome, claimed, err := p.store.Claim(claimCtx, workerID, p.cfg.Lease)
		cancelClaim()
		if err != nil {
			p.limiter.Release()
			if ctx.Err() != nil {
				return
			}
			p.logger.Error("claim job", "error", err, "worker", workerID)
			p.idleSleep(ctx, 2*p.cfg.PollInterval)
			continue
		}

		switch outcome {
		case ClaimNone:
			p.limiter.Release()
			p.idleSleep(ctx, p.cfg.PollInterval)
		case ClaimExhausted:
			p.limiter.Release()
		case ClaimReady:
			p.processClaimed(ctx, claimed)
		}
	}
}

func (p *Pool) processClaimed(ctx context.Context, claimed *ClaimedJob) {
	defer p.limiter.Release()
	p.stats.inFlight.Add(1)
	defer p.stats.inFlight.Add(-1)

	evalCtx, cancelEval := context.WithTimeout(ctx, p.cfg.LLMRequestTimeout)
	defer cancelEval()
	heartbeatCtx, cancelHeartbeat := p.persistenceContext(evalCtx)
	ok, err := p.store.Heartbeat(
		heartbeatCtx,
		claimed.Job.ID,
		claimed.ClaimToken,
		time.Now(),
		time.Now().Add(p.cfg.Lease),
	)
	cancelHeartbeat()
	if err != nil || !ok {
		p.release(ctx, claimed)
		return
	}
	input := evaluationInput(claimed)
	heartbeatStop, claimLost, claimDone := p.heartbeat(ctx, evalCtx, cancelEval, claimed)

	assessment, evalErr := p.service.AssessReview(evalCtx, input)

	// Join the heartbeat so claim loss is visible before finalization.
	heartbeatStop()
	cancelEval()
	<-claimDone

	if ctx.Err() != nil {
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

func (p *Pool) complete(ctx context.Context, claimed *ClaimedJob, assessment moderation.ReviewAssessment) {
	if err := validateAssessment(assessment); err != nil {
		p.finalizeFailed(ctx, claimed, err)
		return
	}
	writeCtx, cancelWrite := p.persistenceContext(ctx)
	defer cancelWrite()
	if err := p.store.Complete(writeCtx, *claimed, assessment); err != nil {
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

func (p *Pool) finalizeFailed(ctx context.Context, claimed *ClaimedJob, evalErr error) {
	class, _, _ := moderation.Classify(evalErr)
	if class == moderation.ErrorClassConfiguration {
		if p.stop != nil {
			p.stop()
		}
		timeout := p.cfg.CleanupTimeout
		if timeout <= 0 {
			timeout = 2 * time.Second
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if _, err := p.store.ReleaseConfigurationFailure(cleanupCtx, claimed.Job.ID, claimed.ClaimToken); err != nil {
			p.logger.Error("record configuration failure and release job", "error", err, "job", claimed.Job.ID.String())
		}
		p.stats.failed.Add(1)
		p.logger.Error("assessment workers stopped; correct runtime settings and restart", "error_class", class)
		return
	}

	if errors.Is(evalErr, ErrClaimLost) {
		return
	}
	if ctx.Err() != nil {
		p.release(ctx, claimed)
		return
	}
	writeCtx, cancelWrite := p.persistenceContext(ctx)
	defer cancelWrite()
	if err := p.store.FinalizeFailed(writeCtx, *claimed, evalErr); err != nil {
		p.logger.Error("finalize failed attempt", "error", err, "job", claimed.Job.ID.String())
		p.release(ctx, claimed)
		return
	}

	errorClass, _, _ := moderation.Classify(evalErr)
	p.stats.failed.Add(1)
	p.logger.Warn("assessment attempt failed", "job", claimed.Job.ID.String(), "error_class", errorClass)
}

// Cancel inference on ownership loss; done joins heartbeat I/O before finalization.
func (p *Pool) heartbeat(
	parentCtx, evalCtx context.Context,
	cancelEval context.CancelFunc,
	claimed *ClaimedJob,
) (stop func(), claimLost <-chan struct{}, done <-chan struct{}) {
	heartbeatCtx, cancelHeartbeat := context.WithCancel(evalCtx)
	claimLostC := make(chan struct{}, 1)
	doneC := make(chan struct{})

	ticker := time.NewTicker(p.cfg.HeartbeatInterval)
	go func() {
		defer ticker.Stop()
		defer close(doneC)
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-parentCtx.Done():
				return
			case <-ticker.C:
				leaseUntil := time.Now().Add(p.cfg.Lease)
				callCtx, cancelCall := p.persistenceContext(heartbeatCtx)
				ok, err := p.store.Heartbeat(callCtx, claimed.Job.ID, claimed.ClaimToken, time.Now(), leaseUntil)
				cancelCall()
				if err != nil || !ok {
					if heartbeatCtx.Err() != nil {
						return
					}
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

	return cancelHeartbeat, claimLostC, doneC
}

func (p *Pool) idleSleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func evaluationInput(claimed *ClaimedJob) moderation.ReviewInput {
	return moderation.ReviewInput{
		ModerationRequestID: claimed.Job.ModerationRequestID.String(),
		ReviewID:            claimed.Job.ReviewID.String(),
		ReviewerID:          claimed.Job.ReviewerID.String(),
		ContentRevision:     claimed.Job.ContentRevision,
		Comment:             claimed.Job.Comment,
	}
}

type poolStats struct {
	inFlight  atomic.Int64
	completed atomic.Int64
	failed    atomic.Int64
}

func (p *Pool) LogStatistics(ctx context.Context) {
	p.logger.InfoContext(ctx, "moderation worker statistics",
		"in_flight", p.stats.inFlight.Load(), "completed", p.stats.completed.Load(),
		"failed_attempts", p.stats.failed.Load())
}

type Assessor interface {
	AssessReview(ctx context.Context, input moderation.ReviewInput) (moderation.ReviewAssessment, error)
}
