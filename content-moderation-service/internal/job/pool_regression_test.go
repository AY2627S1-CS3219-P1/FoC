package job

import (
	"context"
	"errors"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

type repositoryFake struct {
	claim                       func(context.Context) (ClaimOutcome, *ClaimedJob, error)
	heartbeat                   func(context.Context) (bool, error)
	release                     func(context.Context) (bool, error)
	releaseConfigurationFailure func(context.Context) (bool, error)
	complete                    func() error
}

func (f *repositoryFake) Claim(ctx context.Context, _ string, _ time.Duration) (ClaimOutcome, *ClaimedJob, error) {
	return f.claim(ctx)
}

func (f *repositoryFake) Heartbeat(
	ctx context.Context,
	_ uuid.UUID,
	_ uuid.UUID,
	_ time.Time,
	_ time.Time,
) (bool, error) {
	return f.heartbeat(ctx)
}

func (f *repositoryFake) Release(ctx context.Context, _ uuid.UUID, _ uuid.UUID) (bool, error) {
	return f.release(ctx)
}

func (f *repositoryFake) ReleaseConfigurationFailure(ctx context.Context, _ uuid.UUID, _ uuid.UUID) (bool, error) {
	return f.releaseConfigurationFailure(ctx)
}

func (f *repositoryFake) Complete(context.Context, ClaimedJob, moderation.ReviewAssessment) error {
	return f.complete()
}

func (f *repositoryFake) FinalizeFailed(context.Context, ClaimedJob, error) error {
	return errors.New("unexpected failed assessment")
}

type evaluatorFunc func(context.Context, moderation.ReviewInput) (moderation.ReviewAssessment, error)

func (f evaluatorFunc) AssessReview(
	ctx context.Context,
	in moderation.ReviewInput,
) (moderation.ReviewAssessment, error) {
	return f(ctx, in)
}

func regressionConfig() PoolConfig {
	return PoolConfig{
		Concurrency:       1,
		Lease:             time.Minute,
		HeartbeatInterval: time.Millisecond,
		PollInterval:      time.Millisecond,
		LLMRequestTimeout: time.Second,
		CleanupTimeout:    20 * time.Millisecond,
	}
}

func regressionClaim() *ClaimedJob {
	return &ClaimedJob{
		Job:        JobSnapshot{ID: uuid.New(), RunGeneration: 1},
		ClaimToken: uuid.New(),
	}
}

func approvedAssessment() moderation.ReviewAssessment {
	return moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"}
}

func TestStaleClaimIsCheckedBeforeInference(t *testing.T) {
	var evaluated, released bool
	store := &repositoryFake{
		heartbeat: func(context.Context) (bool, error) { return false, nil },
		release:   func(context.Context) (bool, error) { released = true; return false, nil },
	}
	pool := NewPool(
		store,
		evaluatorFunc(func(context.Context, moderation.ReviewInput) (moderation.ReviewAssessment, error) {
			evaluated = true
			return approvedAssessment(), nil
		}),

		regressionConfig(),
		nil,
	)
	pool.processClaimed(context.Background(), regressionClaim())
	if evaluated || !released {
		t.Fatalf("stale inference=%v release=%v", evaluated, released)
	}
}

func TestCompletionCancelsPendingHeartbeatWithoutLosingResult(t *testing.T) {
	entered := make(chan struct{})
	var heartbeats atomic.Int32
	completed := false
	store := &repositoryFake{
		heartbeat: func(ctx context.Context) (bool, error) {
			if heartbeats.Add(1) == 1 {
				return true, nil
			}
			close(entered)
			<-ctx.Done()
			return false, ctx.Err()
		},
		complete: func() error { completed = true; return nil },
		release:  func(context.Context) (bool, error) { t.Error("valid completion released"); return true, nil },
	}
	pool := NewPool(
		store,
		evaluatorFunc(func(ctx context.Context, _ moderation.ReviewInput) (moderation.ReviewAssessment, error) {
			select {
			case <-entered:
				return approvedAssessment(), nil
			case <-ctx.Done():
				return moderation.ReviewAssessment{}, ctx.Err()
			}
		}),

		regressionConfig(),
		nil,
	)
	pool.processClaimed(context.Background(), regressionClaim())
	if !completed {
		t.Fatal("intentional heartbeat cancellation discarded result")
	}
}

func TestFailedTerminalWriteUsesBoundedDetachedCleanup(t *testing.T) {
	released := false
	store := &repositoryFake{
		heartbeat: func(context.Context) (bool, error) { return true, nil },
		complete:  func() error { return errors.New("database unavailable") },
		release: func(ctx context.Context) (bool, error) {
			released = true
			if ctx.Err() != nil {
				t.Error("cleanup inherited canceled context")
			}
			<-ctx.Done()
			return false, ctx.Err()
		},
	}
	pool := NewPool(
		store,
		evaluatorFunc(
			func(context.Context, moderation.ReviewInput) (moderation.ReviewAssessment, error) {
				return approvedAssessment(), nil
			},
		),

		regressionConfig(),
		nil,
	)
	start := time.Now()
	pool.processClaimed(context.Background(), regressionClaim())
	if !released || time.Since(start) > time.Second {
		t.Fatal("cleanup was unbounded")
	}
}

func TestConfigurationFailureReleasesClaimAndStopsPool(t *testing.T) {
	released, stopped := false, false
	p := NewPool(&repositoryFake{releaseConfigurationFailure: func(ctx context.Context) (bool, error) {
		released = true
		if ctx.Err() != nil {
			t.Fatal("cleanup context cancelled")
		}
		return true, nil
	}}, nil, regressionConfig(), nil)
	p.stop = func() { stopped = true }
	p.finalizeFailed(
		context.Background(),
		regressionClaim(),
		moderation.WrapConfigurationError(errors.New("invalid provider settings")),
	)
	if !released || !stopped || p.stats.failed.Load() != 1 {
		t.Fatalf("released=%v stopped=%v", released, stopped)
	}
}
