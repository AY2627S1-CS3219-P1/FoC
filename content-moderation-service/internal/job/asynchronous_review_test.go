//go:build integration

package job_test

import (
	"context"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
)

func TestContendedFinalizationReturnsWorkerCapacity(t *testing.T) {
	db := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(db, moderationjob.DefaultRetrySettings(), nil)
	for range 3 {
		if _, err := store.Ingest(ctx, ingestInput("finalization contention")); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan struct{}, 3)
	unblock := make(chan struct{})
	var calls atomic.Int32
	evaluator := shutdownEvaluator(
		func(ctx context.Context, _ moderation.ReviewInput) (moderation.ReviewAssessment, error) {
			calls.Add(1)
			entered <- struct{}{}
			select {
			case <-unblock:
				return moderation.ReviewAssessment{
					Outcome:       moderation.OutcomeApproved,
					PolicyVersion: "review-text-v1",
				}, nil
			case <-ctx.Done():
				return moderation.ReviewAssessment{}, ctx.Err()
			}
		},
	)
	worker := moderationjob.NewPool(
		store,
		evaluator,
		moderation.NewInferenceLimiter(2),
		moderationjob.PoolConfig{
			Concurrency:       2,
			Lease:             300 * time.Millisecond,
			HeartbeatInterval: 100 * time.Millisecond,
			LLMRequestTimeout: time.Second,
			PollInterval:      time.Millisecond,
			CleanupTimeout:    20 * time.Millisecond,
		},
		nil,
	)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	worker.Start(workerCtx)
	locker, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(ctx); cancel(); worker.Wait() }()
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("workers did not enter inference")
		}
	}
	if _, err := locker.Exec(ctx, `SELECT id FROM moderation_jobs WHERE status='processing' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	close(unblock)
	waitFor(t, func() bool {
		var n int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND state='active' AND wait_event_type='Lock' AND query LIKE '-- name: LockJob%'`).
			Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 2
	}, time.Second, "both workers blocked persisting successful evaluations")
	waitFor(
		t,
		func() bool { return calls.Load() >= 3 },
		time.Second,
		"unlocked job processed despite blocked finalizations",
	)

}

func TestAsyncReviewTerminalOutboxFailureRollsBackResultAndAttempt(t *testing.T) {
	db := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(db, moderationjob.DefaultRetrySettings(), nil)
	if _, err := store.Ingest(ctx, ingestInput("atomic result publication")); err != nil {
		t.Fatal(err)
	}
	claim := mustClaim(t, store, "worker")
	if _, err := db.Exec(
		ctx,
		`ALTER TABLE moderation_outbox ADD CONSTRAINT async_review_fail CHECK(false) NOT VALID`,
	); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = db.Exec(ctx, `ALTER TABLE moderation_outbox DROP CONSTRAINT IF EXISTS async_review_fail`)
	}()
	assessment := moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"}
	if err := store.Complete(ctx, *claim, assessment); err == nil {
		t.Fatal("outbox failure did not abort completion")
	}
	job, err := store.GetJobByID(ctx, claim.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Status != "processing" || job.Outcome != nil || countOutbox(t, db, claim.Job.ID, 1) != 0 {
		t.Fatal("outbox failure left a partial terminal transition")
	}
	var unfinished int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM moderation_attempts WHERE job_id=$1 AND finished_at IS NULL`, claim.Job.ID).
		Scan(&unfinished); err != nil ||
		unfinished != 1 {
		t.Fatalf("attempt was partially finalized: %d,%v", unfinished, err)
	}
	if _, err := db.Exec(ctx, `ALTER TABLE moderation_outbox DROP CONSTRAINT async_review_fail`); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(ctx, *claim, assessment); err != nil {
		t.Fatal(err)
	}
	if countOutbox(t, db, claim.Job.ID, 1) != 1 {
		t.Fatal("completion retry did not commit exactly one event")
	}
	t.Log(
		"CHECKED: failed terminal outbox insert rolled back job and attempt; retry persisted one event without another claim/inference",
	)
}
