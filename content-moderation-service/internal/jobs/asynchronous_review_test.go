//go:build integration

package jobs_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
)

func TestContendedFinalizationReturnsWorkerCapacity(t *testing.T) {
	db := newTestPool(t)
	activateTestConfig(t, db)
	ctx := context.Background()
	store := jobs.NewJobStore(db, jobs.DefaultPolicy(), nil)
	for range 3 {
		if _, err := store.Ingest(ctx, ingestInput("finalization contention")); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan struct{}, 3)
	unblock := make(chan struct{})
	var calls atomic.Int32
	evaluator := shutdownEvaluator(func(ctx context.Context, _ jobs.EvaluationInput) (jobs.Assessment, error) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-unblock:
			return jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"}, nil
		case <-ctx.Done():
			return jobs.Assessment{}, ctx.Err()
		}
	})
	worker := jobs.NewPool(
		store,
		evaluator,
		jobs.NewLimiter(2),
		jobs.PoolConfig{
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

func TestCompletionEnforcesConsistentPolicyProvenance(t *testing.T) {
	db := newTestPool(t)
	activateTestConfig(t, db)
	ctx := context.Background()
	store := jobs.NewJobStore(db, jobs.DefaultPolicy(), nil)
	if _, err := store.Ingest(ctx, ingestInput("policy provenance")); err != nil {
		t.Fatal(err)
	}
	claim := mustClaim(t, store, "worker")
	if err := store.Complete(
		ctx,
		*claim,
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v2"},
	); err == nil {
		t.Fatal("mismatched policy accepted")
	}
	if countOutbox(t, db, claim.Job.ID, 1) != 0 {
		t.Fatal("mismatched policy emitted event")
	}
	if err := store.Complete(
		ctx,
		*claim,
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); err != nil {
		t.Fatal(err)
	}

	var stored, published string
	var version int
	if err := db.QueryRow(ctx, `SELECT j.policy_version,o.payload->>'policyVersion',o.payload_version FROM moderation_jobs j JOIN moderation_outbox o ON o.job_id=j.id WHERE j.id=$1`, claim.Job.ID).
		Scan(&stored, &published, &version); err != nil {
		t.Fatal(err)
	}
	if stored != "review-text-v1" || published != stored || version != 1 {
		t.Fatalf("did not reproduce: stored=%s published=%s", stored, published)
	}
	t.Logf("Verified: stored policy=%s, outgoing policy=%s, payload_version=%d", stored, published, version)
}

func TestAsyncReviewTerminalOutboxFailureRollsBackResultAndAttempt(t *testing.T) {
	db := newTestPool(t)
	activateTestConfig(t, db)
	ctx := context.Background()
	store := jobs.NewJobStore(db, jobs.DefaultPolicy(), nil)
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
	assessment := jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"}
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

func TestWorkerPausesPolicyMismatchWithoutPublishing(t *testing.T) {
	db := newTestPool(t)
	activateTestConfig(t, db)
	ctx := context.Background()
	store := jobs.NewJobStore(db, jobs.DefaultPolicy(), nil)
	in := ingestInput("changed evaluator policy")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	evaluator := shutdownEvaluator(func(context.Context, jobs.EvaluationInput) (jobs.Assessment, error) {
		return jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v2"}, nil
	})
	worker := jobs.NewPool(
		store,
		evaluator,
		jobs.NewLimiter(1),
		jobs.PoolConfig{
			Concurrency:       1,
			Lease:             time.Minute,
			HeartbeatInterval: time.Second,
			LLMRequestTimeout: time.Second,
			PollInterval:      time.Millisecond,
		},
		nil,
	)
	workerCtx, cancel := context.WithCancel(ctx)
	worker.Start(workerCtx)
	defer func() { cancel(); worker.Wait() }()
	waitFor(
		t,
		func() bool { return countWhere(t, db, `SELECT count(*) FROM moderation_config_pauses`) == 1 },
		time.Second,
		"policy mismatch paused",
	)
	job := jobByRequest(t, db, in.ModerationRequestID)
	if job.Status != "retry_wait" || job.AttemptCount != 1 || countOutbox(t, db, job.ID, 1) != 0 {
		t.Fatal("mismatched policy was published or retried without configuration recovery")
	}
}
