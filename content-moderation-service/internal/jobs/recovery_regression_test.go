//go:build integration

package jobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	"github.com/jackc/pgx/v5"
)

func TestOutboxLeaseExcludesLiveOwnersAndFencesExpiredOwners(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	if _, err := store.Ingest(ctx, ingestInput("outbox")); err != nil {
		t.Fatal(err)
	}
	c := mustClaim(t, store, "worker")
	if err := store.Complete(
		ctx,
		*c,
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
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
	activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.PolicyFromConfig(2, time.Millisecond, time.Millisecond), nil)
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
		if outcome != jobs.ClaimReady && outcome != jobs.ClaimExhausted {
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
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); !errors.Is(
		err,
		jobs.ErrFenced,
	) {
		t.Fatalf("abandoned owner wrote: %v", err)
	}
	if outcome, _, err := store.Claim(ctx, "again", time.Minute); err != nil || outcome != jobs.ClaimNone {
		t.Fatalf("terminal job reclaimed: %v, %v", outcome, err)
	}
}

func TestPauseRecoveryIncludesTransientRetriesAndReleasedJobs(t *testing.T) {
	pool := newTestPool(t)
	first := activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	for _, text := range []string{"configuration", "transient", "released", "in-flight"} {
		if _, err := store.Ingest(ctx, ingestInput(text)); err != nil {
			t.Fatal(err)
		}
	}
	a, b, c := mustClaim(t, store, "a"), mustClaim(t, store, "b"), mustClaim(t, store, "c")
	stale := mustClaim(t, store, "in-flight")
	if err := store.FinalizeFailed(ctx, *b, jobs.WrapServerError(errors.New("transient"))); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Release(ctx, c.Job.ID, c.ClaimToken); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeFailed(ctx, *a, jobs.WrapConfigurationError(errors.New("unsupported"))); err != nil {
		t.Fatal(err)
	}
	configs := config.NewStore(pool)
	second, err := configs.InsertConfigVersion(
		ctx,
		"corrected",
		[]byte(`{}`),
		config.OutputModeJSONObject,
		"review-text-v1",
		"new",
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := configs.ActivateAndResume(ctx, second, "admin", &first, true); err != nil {
		t.Fatal(err)
	}
	if err := store.FinalizeFailed(
		ctx,
		*stale,
		jobs.WrapConfigurationError(errors.New("late response")),
	); !errors.Is(
		err,
		jobs.ErrFenced,
	) {
		t.Fatalf("superseded inference changed pause: %v", err)
	}
	for range 4 {
		resumed := mustClaim(t, store, "resumed")
		if resumed.Job.RunGeneration != 2 || resumed.AttemptNumber != 1 || resumed.Config.VersionID != second {
			t.Fatalf("incorrect recovery: %+v", resumed)
		}
		if err := store.Complete(
			ctx,
			*resumed,
			jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
		); err != nil {
			t.Fatal(err)
		}
	}
	if outcome, _, err := store.Claim(ctx, "empty", time.Minute); err != nil || outcome != jobs.ClaimNone {
		t.Fatalf("stranded work: %v,%v", outcome, err)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM moderation_admin_audit WHERE action='RESUME_CONFIGURATION_PAUSED_JOB'`).
		Scan(&audits); err != nil ||
		audits != 4 {
		t.Fatalf("audit count = %d, %v", audits, err)
	}
}

func TestProviderChangeWaitsForNewValidatedConfiguration(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	configs := config.NewStore(pool)
	if err := configs.EnsureConfigState(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := configs.InsertConfigVersion(
		ctx,
		"model",
		[]byte(`{}`),
		config.OutputModeJSONObject,
		"review-text-v1",
		llmpolicy.ProviderFingerprint("https://old.example/v1", "model"),
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := configs.ActivateConfigVersion(ctx, old, "admin", nil); err != nil {
		t.Fatal(err)
	}
	store := jobs.NewJobStore(
		pool,
		jobs.DefaultPolicy(),
		nil,
		jobs.WithProviderIdentity("https://new.example/v1", llmpolicy.ProviderFingerprint),
	)
	in := ingestInput("provider changed")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	if outcome, _, err := store.Claim(ctx, "worker", time.Minute); err != nil || outcome != jobs.ClaimNoConfiguration {
		t.Fatalf("old provider configuration executed: %v,%v", outcome, err)
	}
	if job := jobByRequest(t, pool, in.ModerationRequestID); job.AttemptCount != 0 {
		t.Fatal("provider mismatch consumed an inference attempt")
	}
	updated, err := configs.InsertConfigVersion(
		ctx,
		"model",
		[]byte(`{}`),
		config.OutputModeJSONObject,
		"review-text-v1",
		llmpolicy.ProviderFingerprint("https://new.example/v1", "model"),
		"admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := configs.ActivateAndResume(ctx, updated, "admin", &old, true); err != nil {
		t.Fatal(err)
	}
	if c := mustClaim(t, store, "validated"); c.Config.VersionID != updated {
		t.Fatal("provider change did not bind corrected version")
	}
}

type shutdownEvaluator func(context.Context, jobs.EvaluationInput) (jobs.Assessment, error)

func (f shutdownEvaluator) Evaluate(ctx context.Context, in jobs.EvaluationInput) (jobs.Assessment, error) {
	return f(ctx, in)
}
func TestShutdownReleaseIsBoundedWhileDatabaseRowIsLocked(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	in := ingestInput("shutdown")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	eval := shutdownEvaluator(func(ctx context.Context, _ jobs.EvaluationInput) (jobs.Assessment, error) {
		close(entered)
		<-ctx.Done()
		return jobs.Assessment{}, ctx.Err()
	})
	p := jobs.NewPool(
		store,
		eval,
		jobs.NewLimiter(1),
		jobs.PoolConfig{
			Concurrency:       1,
			CleanupTimeout:    30 * time.Millisecond,
			Lease:             time.Minute,
			HeartbeatInterval: 10 * time.Second,
			PollInterval:      time.Millisecond,
			LLMRequestTimeout: time.Minute,
			Policy:            jobs.DefaultPolicy(),
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
	activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.PolicyFromConfig(2, time.Millisecond, time.Millisecond), nil)
	if _, err := store.Ingest(ctx, ingestInput("release budget")); err != nil {
		t.Fatal(err)
	}
	var id jobs.ClaimedJob
	for range 2 {
		claimed := mustClaim(t, store, "released")
		id = *claimed
		if ok, err := store.Release(ctx, claimed.Job.ID, claimed.ClaimToken); err != nil || !ok {
			t.Fatalf("release: %v,%v", ok, err)
		}
	}
	if outcome, _, err := store.Claim(ctx, "exhaust", time.Minute); err != nil || outcome != jobs.ClaimExhausted {
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
	activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
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
		done <- store.FinalizeFailed(finishCtx, *claimed, jobs.WrapServerError(errors.New("transient")))
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

func TestOccupiedInferenceGateLeavesJobAvailableToAnotherReplica(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	in := ingestInput("shared inference capacity")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	gate := jobs.NewLimiter(1)
	if err := gate.Acquire(ctx); err != nil {
		t.Fatal(err)
	}
	evaluated := make(chan struct{}, 1)
	evaluator := shutdownEvaluator(func(context.Context, jobs.EvaluationInput) (jobs.Assessment, error) {
		evaluated <- struct{}{}
		return jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"}, nil
	})
	worker := jobs.NewPool(
		store,
		evaluator,
		gate,
		jobs.PoolConfig{
			Concurrency:       1,
			Lease:             time.Minute,
			HeartbeatInterval: time.Second,
			PollInterval:      time.Millisecond,
			LLMRequestTimeout: time.Second,
		},
		nil,
	)
	workerCtx, cancel := context.WithCancel(ctx)
	worker.Start(workerCtx)
	defer func() { cancel(); gate.Release(); worker.Wait() }()
	time.Sleep(30 * time.Millisecond)
	if job := jobByRequest(t, pool, in.ModerationRequestID); job.Status != "queued" || job.AttemptCount != 0 {
		t.Fatalf("gate waiter consumed durable claim: %+v", job)
	}
	other := mustClaim(t, store, "other-replica")
	gate.Release()
	select {
	case <-evaluated:
		t.Fatal("gate waiter inferred with work owned by another replica")
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	worker.Wait()
	if err := store.Complete(
		ctx,
		*other,
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); err != nil {
		t.Fatal(err)
	}
}

func TestJobWritesRejectLeaseExpiryDuringRowLockWait(t *testing.T) {
	for _, operation := range []string{"complete", "heartbeat", "release", "failure"} {
		t.Run(operation, func(t *testing.T) {
			pool := newTestPool(t)
			activateTestConfig(t, pool)
			ctx := context.Background()
			store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
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
					done <- store.Complete(ctx, *claimed, jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"})
				case "failure":
					done <- store.FinalizeFailed(ctx, *claimed, jobs.WrapServerError(errors.New("provider failure")))
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
				if !errors.Is(err, jobs.ErrFenced) {
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
