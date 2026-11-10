//go:build integration

package job_test

import (
	"context"
	"errors"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"github.com/jackc/pgx/v5"
)

func TestOutboxLeaseExcludesLiveOwnersAndFencesExpiredOwners(t *testing.T) {
	pool := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
	if _, err := store.Ingest(ctx, ingestInput("outbox")); err != nil {
		t.Fatal(err)
	}
	c := mustClaim(t, store, "worker")
	if err := store.Complete(
		ctx,
		*c,
		moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); err != nil {
		t.Fatal(err)
	}
	q := moderationdb.New(pool)
	first, err := q.ClaimOutboxLease(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	if first.LeaseUntil == nil || time.Until(*first.LeaseUntil) < 59*time.Second {
		t.Fatal("configured relay lease ignored")
	}
	if _, err := q.ClaimOutboxLease(ctx, 60); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("live lease stolen: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE moderation_outbox SET lease_until=now()-interval '1 second' WHERE event_id=$1`,
		first.EventID,
	); err != nil {
		t.Fatal(err)
	}
	second, err := q.ClaimOutboxLease(ctx, 60)
	if err != nil {
		t.Fatal(err)
	}
	if second.EventID != first.EventID || *second.LeaseToken == *first.LeaseToken {
		t.Fatal("expired lease was not recovered")
	}
	if rows, err := q.MarkOutboxPublished(
		ctx,
		moderationdb.MarkOutboxPublishedParams{EventID: first.EventID, LeaseToken: first.LeaseToken},
	); err != nil ||
		rows != 0 {
		t.Fatalf("stale published mark: %d, %v", rows, err)
	}
	if rows, err := q.MarkOutboxPublished(
		ctx,
		moderationdb.MarkOutboxPublishedParams{EventID: second.EventID, LeaseToken: second.LeaseToken},
	); err != nil ||
		rows != 1 {
		t.Fatalf("current published mark: %d, %v", rows, err)
	}
}

func TestCrashRecoveryEndsAtAttemptBudgetWithOneTerminalOutbox(t *testing.T) {
	pool := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(
		pool,
		moderationjob.RetrySettingsFromConfig(2, time.Millisecond, time.Millisecond),
		nil,
		"test-model")
	in := ingestInput("repeated crash")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	first := mustClaim(t, store, "first")
	for range 2 {
		if _, err := pool.Exec(
			ctx,
			`UPDATE moderation_jobs SET lease_until=now()-interval '1 second' WHERE moderation_request_id=$1`,
			in.ModerationRequestID,
		); err != nil {
			t.Fatal(err)
		}
		outcome, _, err := store.Claim(ctx, "recover", time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if outcome != moderationjob.ClaimReady && outcome != moderationjob.ClaimExhausted {
			t.Fatalf("unexpected recovery: %v", outcome)
		}
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "errored" || job.AttemptCount != 2 || job.ClaimToken != nil {
		t.Fatalf("unbounded recovery: %+v", job)
	}
	if countOutbox(t, pool, job.ID, 1) != 1 {
		t.Fatal("budget exhaustion must enqueue exactly one ERROR")
	}
	if err := store.Complete(
		ctx,
		*first,
		moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); !errors.Is(
		err,
		moderationjob.ErrFenced,
	) {
		t.Fatalf("abandoned owner wrote: %v", err)
	}
	if outcome, _, err := store.Claim(ctx, "again", time.Minute); err != nil || outcome != moderationjob.ClaimNone {
		t.Fatalf("terminal job reclaimed: %v, %v", outcome, err)
	}
}

type shutdownEvaluator func(context.Context, moderation.ReviewInput) (moderation.ReviewAssessment, error)

func (f shutdownEvaluator) AssessReview(
	ctx context.Context,
	in moderation.ReviewInput,
) (moderation.ReviewAssessment, error) {
	return f(ctx, in)
}
func TestShutdownReleaseIsBoundedWhileDatabaseRowIsLocked(t *testing.T) {
	pool := newTestPool(t)

	ctx, cancel := context.WithCancel(context.Background())
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
	in := ingestInput("shutdown")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	eval := shutdownEvaluator(func(ctx context.Context, _ moderation.ReviewInput) (moderation.ReviewAssessment, error) {
		close(entered)
		<-ctx.Done()
		return moderation.ReviewAssessment{}, ctx.Err()
	})
	p := moderationjob.NewPool(
		store,
		eval,

		moderationjob.PoolConfig{
			Concurrency:       1,
			CleanupTimeout:    30 * time.Millisecond,
			Lease:             time.Minute,
			HeartbeatInterval: 10 * time.Second,
			PollInterval:      time.Millisecond,
			LLMRequestTimeout: time.Minute,
			RetrySettings:     moderationjob.DefaultRetrySettings(),
		},
		nil,
	)
	p.Start(ctx)
	select {
	case <-entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("no evaluation")
	}
	locker, err := pool.Begin(context.Background())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := locker.Exec(
		context.Background(),
		`SELECT id FROM moderation_jobs WHERE moderation_request_id=$1 FOR UPDATE`,
		in.ModerationRequestID,
	); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	done := make(chan struct{})
	go func() { p.Wait(); close(done) }()

	defer func() { _ = locker.Rollback(context.Background()) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		_ = locker.Rollback(context.Background())
		<-done
		t.Fatal("shutdown waited for external transaction to unlock row")
	}
}

func TestRepeatedShutdownReleaseCannotBypassAttemptBudget(t *testing.T) {
	pool := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(
		pool,
		moderationjob.RetrySettingsFromConfig(2, time.Millisecond, time.Millisecond),
		nil,
		"test-model")
	if _, err := store.Ingest(ctx, ingestInput("release budget")); err != nil {
		t.Fatal(err)
	}
	var id moderationjob.ClaimedJob
	for range 2 {
		claimed := mustClaim(t, store, "released")
		id = *claimed
		if ok, err := store.Release(ctx, claimed.Job.ID, claimed.ClaimToken); err != nil || !ok {
			t.Fatalf("release: %v,%v", ok, err)
		}
	}
	if outcome, _, err := store.Claim(
		ctx,
		"exhaust",
		time.Minute,
	); err != nil ||
		outcome != moderationjob.ClaimExhausted {
		t.Fatalf("released attempts exceeded budget: %v,%v", outcome, err)
	}
	var finished int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM moderation_attempts WHERE job_id=$1 AND finished_at IS NOT NULL AND outcome='released'`, id.Job.ID).
		Scan(&finished); err != nil ||
		finished != 2 {
		t.Fatalf("released history: %d,%v", finished, err)
	}
	if countOutbox(t, pool, id.Job.ID, 1) != 1 {
		t.Fatal("release exhaustion omitted terminal ERROR")
	}
}

func TestFailureFinalizationLocksJobBeforeAttemptHistory(t *testing.T) {
	pool := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
	if _, err := store.Ingest(ctx, ingestInput("lock order")); err != nil {
		t.Fatal(err)
	}
	claimed := mustClaim(t, store, "worker")
	locker, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = locker.Rollback(ctx) }()
	if _, err := locker.Exec(ctx, `SELECT id FROM moderation_jobs WHERE id=$1 FOR UPDATE`, claimed.Job.ID); err != nil {
		t.Fatal(err)
	}
	finishCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- store.FinalizeFailed(finishCtx, *claimed, moderation.WrapServerError(errors.New("transient")))
	}()
	waitFor(t, func() bool {
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND state='active' AND wait_event_type='Lock' AND query LIKE '%moderation_jobs%')`).
			Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		return waiting
	}, time.Second, "finalizer waiting for job lock")
	// Admin recovery holds the job first and then finishes history. Finalization
	// must not lock that history while waiting for the job in the opposite order.
	updateCtx, cancelUpdate := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancelUpdate()
	if _, err := locker.Exec(
		updateCtx,
		`UPDATE moderation_attempts SET detail=detail WHERE job_id=$1`,
		claimed.Job.ID,
	); err != nil {
		t.Fatalf("attempt locked before job: %v", err)
	}
	if err := locker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestJobWritesRejectLeaseExpiryDuringRowLockWait(t *testing.T) {
	for _, operation := range []string{"complete", "heartbeat", "release", "failure"} {
		t.Run(operation, func(t *testing.T) {
			pool := newTestPool(t)

			ctx := context.Background()
			store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil, "test-model")
			if _, err := store.Ingest(ctx, ingestInput("blocked transition")); err != nil {
				t.Fatal(err)
			}
			_, claimed, err := store.Claim(ctx, "worker", 400*time.Millisecond)
			if err != nil || claimed == nil {
				t.Fatalf("claim: %v", err)
			}
			locker, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = locker.Rollback(ctx) }()
			if _, err := locker.Exec(
				ctx,
				`SELECT id FROM moderation_jobs WHERE id=$1 FOR UPDATE`,
				claimed.Job.ID,
			); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() {
				switch operation {
				case "complete":
					done <- store.Complete(ctx, *claimed, moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"})
				case "failure":
					done <- store.FinalizeFailed(ctx, *claimed, moderation.WrapServerError(errors.New("provider failure")))
				case "heartbeat":
					ok, err := store.Heartbeat(
						ctx,
						claimed.Job.ID,
						claimed.ClaimToken,
						time.Now(),
						time.Now().Add(time.Minute),
					)
					if ok {
						err = errors.New("heartbeat accepted expired lease")
					}
					done <- err
				case "release":
					ok, err := store.Release(ctx, claimed.Job.ID, claimed.ClaimToken)
					if ok {
						err = errors.New("release accepted expired lease")
					}
					done <- err
				}
			}()
			waitFor(t, func() bool {
				var waiting bool
				if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND pid<>pg_backend_pid() AND state='active' AND wait_event_type='Lock' AND query LIKE '%moderation_jobs%')`).
					Scan(&waiting); err != nil {
					t.Fatal(err)
				}
				return waiting
			}, time.Second, "transition waiting for job lock")
			time.Sleep(450 * time.Millisecond)
			if err := locker.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if operation == "complete" || operation == "failure" {
				if !errors.Is(err, moderationjob.ErrFenced) {
					t.Fatalf("expired %s accepted: %v", operation, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			job, err := store.GetJobByID(ctx, claimed.Job.ID)
			if err != nil {
				t.Fatal(err)
			}
			if job.Status != "processing" || countOutbox(t, pool, claimed.Job.ID, 1) != 0 {
				t.Fatal("expired owner changed durable job state")
			}
		})
	}
}
