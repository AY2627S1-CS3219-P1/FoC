//go:build integration

// Integration tests for the durable moderation engine against a real
// PostgreSQL. Set TEST_DATABASE_URL to a throwaway database; the tests apply
// the module migrations and truncate all tables between subtests.
package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/config"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/review"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestPool migrates the throwaway database and returns a connected pool.
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := envOrSkip(t)
	ctx := context.Background()
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool := database.Connect(url)
	t.Cleanup(pool.Close)
	truncateAll(t, pool)
	return pool
}

func envOrSkip(t *testing.T) string {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	return url
}

func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		TRUNCATE moderation_jobs, moderation_attempts, moderation_outbox,
			moderation_quarantine, moderation_config_pauses, moderation_config_versions,
			moderation_config_state, moderation_admin_audit`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// activateTestConfig inserts and activates one configuration version so claims
// can pin it.
func activateTestConfig(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	store := config.NewStore(pool)
	if err := store.EnsureConfigState(context.Background()); err != nil {
		t.Fatalf("ensure config state: %v", err)
	}
	id, err := store.InsertConfigVersion(
		context.Background(), "test-model", []byte(`{}`), config.OutputModeJSONSchema,
		"review-text-v1", "fp-test", "tester",
	)
	if err != nil {
		t.Fatalf("insert config version: %v", err)
	}
	if err := store.ActivateConfigVersion(context.Background(), id, "tester", nil); err != nil {
		t.Fatalf("activate config: %v", err)
	}
	return id
}

func ingestInput(comment string) jobs.IngestionInput {
	return jobs.IngestionInput{
		SourceEventID:       uuid.New(),
		ModerationRequestID: uuid.New(),
		ReviewID:            uuid.New(),
		ReviewerID:          uuid.New(),
		ContentRevision:     1,
		Comment:             comment,
	}
}

func jobIDByRequest(t *testing.T, pool *pgxpool.Pool, requestID uuid.UUID) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	err := pool.QueryRow(context.Background(),
		`SELECT id FROM moderation_jobs WHERE moderation_request_id = $1`, requestID).Scan(&id)
	if err != nil {
		t.Fatalf("load job id: %v", err)
	}
	return id
}

func jobByRequest(t *testing.T, pool *pgxpool.Pool, requestID uuid.UUID) moderationdb.ModerationJob {
	t.Helper()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	job, err := store.GetJobByID(context.Background(), jobIDByRequest(t, pool, requestID))
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	return job
}

func countWhere(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count (%s): %v", query, err)
	}
	return n
}

func countJobs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	return countWhere(t, pool, `SELECT count(*) FROM moderation_jobs`)
}

func countQuarantined(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	return countWhere(t, pool, `SELECT count(*) FROM moderation_quarantine`)
}

func countOutbox(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID, generation int32) int {
	t.Helper()
	if bad := countWhere(
		t,
		pool,
		`SELECT count(*) FROM moderation_outbox WHERE job_id=$1 AND run_generation=$2 AND payload_version<>1`,
		jobID,
		generation,
	); bad != 0 {
		t.Fatal("terminal event has unsupported payload version")
	}

	return countWhere(t, pool,
		`SELECT count(*) FROM moderation_outbox WHERE job_id = $1 AND run_generation = $2`,
		jobID, generation)
}

func countAttempts(t *testing.T, pool *pgxpool.Pool, jobID uuid.UUID, generation int32) int {
	t.Helper()
	return countWhere(t, pool,
		`SELECT count(*) FROM moderation_attempts WHERE job_id = $1 AND run_generation = $2`,
		jobID, generation)
}

func mustClaim(t *testing.T, store *jobs.JobStore, workerID string) *jobs.ClaimedJob {
	t.Helper()
	outcome, claimed, err := store.Claim(context.Background(), workerID, 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if outcome != jobs.ClaimReady {
		t.Fatalf("claim outcome = %v, want ready", outcome)
	}
	return claimed
}

// forceDue makes every non-terminal job immediately claimable, removing jitter
// from the retry-policy assertions.
func forceDue(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`UPDATE moderation_jobs SET next_attempt_at = now() - interval '1s'
		 WHERE status IN ('queued','retry_wait')`)
	if err != nil {
		t.Fatalf("force due: %v", err)
	}
}

// claimDue force-due and claims one job.
func claimDue(t *testing.T, store *jobs.JobStore, pool *pgxpool.Pool) *jobs.ClaimedJob {
	t.Helper()
	forceDue(t, pool)
	return mustClaim(t, store, "retry-worker")
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool, within time.Duration, what string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition %q not met within %v", what, within)
}

func TestIngestionDeduplicatesAndQuarantines(t *testing.T) {
	pool := newTestPool(t)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	t.Run("first delivery inserts one queued job", func(t *testing.T) {
		in := ingestInput("a calm review")
		result, err := store.Ingest(ctx, in)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if result != jobs.IngestionInserted {
			t.Fatalf("result = %v, want inserted", result)
		}
		job := jobByRequest(t, pool, in.ModerationRequestID)
		if job.Status != "queued" || job.RunGeneration != 1 || job.AttemptCount != 0 {
			t.Fatalf("seeded job wrong: %+v", job)
		}
		if job.Comment != in.Comment || job.ReviewerID != in.ReviewerID {
			t.Fatalf("snapshot mismatch: %+v", job)
		}
	})

	t.Run("duplicate delivery with agreeing fields is a no-op", func(t *testing.T) {
		in := ingestInput("second review")
		if _, err := store.Ingest(ctx, in); err != nil {
			t.Fatalf("first ingest: %v", err)
		}
		result, err := store.Ingest(ctx, in) // same event redelivered
		if err != nil {
			t.Fatalf("duplicate ingest: %v", err)
		}
		if result != jobs.IngestionDeduplicated {
			t.Fatalf("result = %v, want deduplicated", result)
		}
		if countJobs(t, pool) != 2 { // one from the first subtest + this one
			t.Fatalf("duplicate delivery created another job: %d", countJobs(t, pool))
		}
		if n := countWhere(t, pool,
			`SELECT count(*) FROM moderation_jobs WHERE moderation_request_id = $1`,
			in.ModerationRequestID); n != 1 {
			t.Fatalf("duplicate delivery produced %d jobs for one request id", n)
		}
	})

	t.Run("disagreeing duplicate is quarantined", func(t *testing.T) {
		in := ingestInput("original comment")
		if _, err := store.Ingest(ctx, in); err != nil {
			t.Fatalf("first ingest: %v", err)
		}
		conflict := in
		conflict.ReviewerID = uuid.New()
		result, err := store.Ingest(ctx, conflict)
		if err != nil {
			t.Fatalf("conflict ingest must ack: %v", err)
		}
		if result != jobs.IngestionConflictQuarantined {
			t.Fatalf("result = %v, want conflict_quarantined", result)
		}
		if countQuarantined(t, pool) == 0 {
			t.Fatalf("conflict was not quarantined")
		}
	})

	t.Run("malformed event is quarantined and acknowledged", func(t *testing.T) {
		before := countQuarantined(t, pool)
		err := store.QuarantineMalformed(ctx, jobs.MalformedInput{
			Topic: jobs.UsersReviewSubmittedTopic, MessageKey: "not-a-uuid",
			EventID: "evt-1", Reason: "unparseable identifiers", Comment: "",
		})
		if err != nil {
			t.Fatalf("quarantine malformed: %v", err)
		}
		if countQuarantined(t, pool) != before+1 {
			t.Fatalf("malformed event was not recorded")
		}
	})
}

func TestClaimsAreExclusiveAndCoverTheQueue(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	const total = 6
	requests := make([]uuid.UUID, 0, total)
	for i := range total {
		in := ingestInput(fmt.Sprintf("review %d", i))
		if _, err := store.Ingest(ctx, in); err != nil {
			t.Fatalf("ingest: %v", err)
		}
		requests = append(requests, in.ModerationRequestID)
	}

	// Two claim loops race over the same queue with SKIP LOCKED semantics.
	var mu sync.Mutex
	seen := make(map[uuid.UUID]bool)
	var wg sync.WaitGroup
	claimOne := func(worker string) {
		defer wg.Done()
		outcome, claimed, err := store.Claim(ctx, worker, 30*time.Second)
		if err != nil {
			t.Errorf("claim: %v", err)
			return
		}
		if outcome != jobs.ClaimReady || claimed == nil {
			t.Errorf("unexpected claim outcome %v", outcome)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if seen[claimed.Job.ModerationRequestID] {
			t.Errorf("job %s claimed twice", claimed.Job.ModerationRequestID)
		}
		seen[claimed.Job.ModerationRequestID] = true
	}
	for i := range total {
		wg.Add(1)
		if i%2 == 0 {
			go claimOne("worker-a")
		} else {
			claimOne("worker-b")
		}
	}
	wg.Wait()

	if len(seen) != total {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), total)
	}
	for _, id := range requests {
		if !seen[id] {
			t.Fatalf("job %s was never claimed", id)
		}
	}
}

func TestStaleClaimCannotFinalize(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("fencing target")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")

	// A stale worker (wrong claim token) must not complete the job.
	stale := *claimed
	stale.ClaimToken = uuid.New()
	err := store.Complete(ctx, stale, jobs.Assessment{
		Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1",
	})
	if !errors.Is(err, jobs.ErrFenced) {
		t.Fatalf("stale complete error = %v, want ErrFenced", err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "processing" || job.Outcome != nil {
		t.Fatalf("stale completion mutated the job: %+v", job)
	}

	// The rightful owner completes afterwards.
	if err := store.Complete(ctx, *claimed, jobs.Assessment{
		Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1",
	}); err != nil {
		t.Fatalf("rightful complete: %v", err)
	}
	job = jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "completed" || job.Outcome == nil || *job.Outcome != "approved" {
		t.Fatalf("completion did not land: %+v", job)
	}
	if len(job.ReasonCodes) != 0 {
		t.Fatalf("approval must carry no reason codes: %v", job.ReasonCodes)
	}
	if job.ClaimToken != nil {
		t.Fatalf("terminal transition must clear the claim: %+v", job.ClaimToken)
	}
	if countOutbox(t, pool, job.ID, job.RunGeneration) != 1 {
		t.Fatalf("terminal completion must enqueue exactly one outbox event")
	}
}

func TestExpiredLeaseRecoveryRecordsAbandonedAttempt(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("recover me")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if _, _, err := store.Claim(ctx, "crashed-worker", time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	jobID := jobIDByRequest(t, pool, in.ModerationRequestID)
	if n := countAttempts(t, pool, jobID, 1); n != 1 {
		t.Fatalf("attempt rows after first claim = %d, want 1", n)
	}

	// Simulate a crashed worker: let the lease expire.
	if _, err := pool.Exec(ctx,
		`UPDATE moderation_jobs SET lease_until = now() - interval '1s' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	outcome, recovered, err := store.Claim(ctx, "recovery-worker", 30*time.Second)
	if err != nil || outcome != jobs.ClaimReady || recovered == nil {
		t.Fatalf("recovery claim: %v %v %v", outcome, recovered, err)
	}
	if recovered.Job.ModerationRequestID != in.ModerationRequestID {
		t.Fatalf("recovered the wrong job")
	}
	if recovered.Job.RunGeneration != 1 {
		t.Fatalf("automatic recovery must not change the generation: %d", recovered.Job.RunGeneration)
	}
	// The abandoned attempt stays recorded: two attempt rows for generation 1.
	// Recovery finishes the abandoned attempt with an explicit crash outcome;
	// the new attempt remains open until its owner finishes.
	if n := countAttempts(t, pool, jobID, 1); n != 2 {
		t.Fatalf("attempt rows = %d, want 2 (abandoned + recovery)", n)
	}
	var abandonedFinished int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM moderation_attempts
		 WHERE job_id = $1 AND run_generation = 1 AND attempt_number = 1 AND finished_at IS NOT NULL AND outcome = 'abandoned'`,
		jobID).Scan(&abandonedFinished); err != nil {
		t.Fatalf("count abandoned: %v", err)
	}
	if abandonedFinished != 1 {
		t.Fatalf("abandoned attempt must be finalized as abandoned")
	}
}

func TestRetryBudgetAndTerminalOutbox(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	policy := jobs.DefaultPolicy()
	store := jobs.NewJobStore(pool, policy, nil)
	ctx := context.Background()

	in := ingestInput("doomed review")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")

	// A durable failure inside the budget schedules a delayed retry. The wait
	// is fully jittered in [0, initial] for the first attempt, so only the
	// bounds are asserted.
	if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapConnectionError(errors.New("boom"))); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	now := time.Now()
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "retry_wait" {
		t.Fatalf("status = %q, want retry_wait", job.Status)
	}
	if job.NextAttemptAt == nil {
		t.Fatalf("retry must carry next_attempt_at")
	}
	if job.NextAttemptAt.Before(now.Add(-time.Second)) ||
		job.NextAttemptAt.After(now.Add(policy.InitialDelay+2*time.Second)) {
		t.Fatalf("retry wait %v outside the jittered [0, initial] window relative to %v",
			job.NextAttemptAt.Sub(now), now)
	}
	if job.ClaimToken != nil {
		t.Fatalf("retry must clear the claim")
	}
	if countOutbox(t, pool, job.ID, job.RunGeneration) != 0 {
		t.Fatalf("non-terminal failure must not enqueue an outbox event")
	}

	// Exhaust the budget: attempts 1..4 retry, attempt 5 (MaxAttempts) errors.
	for attempt := 1; ; attempt++ {
		if attempt > policy.MaxAttempts {
			t.Fatalf("job never became errored after %d attempts", policy.MaxAttempts)
		}
		claimed := claimDue(t, store, pool)
		if claimed.Job.ID != job.ID {
			t.Fatalf("claimed unexpected job %s", claimed.Job.ID)
		}
		if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapConnectionError(errors.New("boom"))); err != nil {
			t.Fatalf("finalize attempt %d: %v", attempt, err)
		}
		job = jobByRequest(t, pool, in.ModerationRequestID)
		if job.Status == "errored" {
			break
		}
		if job.Status != "retry_wait" {
			t.Fatalf("unexpected status %q", job.Status)
		}
	}
	if int(job.AttemptCount) != policy.MaxAttempts {
		t.Fatalf("attempt count = %d, want %d", job.AttemptCount, policy.MaxAttempts)
	}
	if job.Outcome != nil {
		t.Fatalf("errored jobs must not carry a content outcome: %v", job.Outcome)
	}
	if job.ErrorClass == nil || *job.ErrorClass != jobs.ErrorClassConnection {
		t.Fatalf("errored job must record the sanitized error class: %v", job.ErrorClass)
	}
	if countOutbox(t, pool, job.ID, job.RunGeneration) != 1 {
		t.Fatalf("terminal transition must commit exactly one outbox row")
	}

	// A terminal job is not claimable again even when due.
	forceDue(t, pool)
	outcome, claimed2, err := store.Claim(ctx, "worker-b", 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if outcome == jobs.ClaimReady && claimed2 != nil && claimed2.Job.ID == job.ID {
		t.Fatalf("terminal job was claimed again")
	}
}

func TestInvalidOutputBudgetEndsInError(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	policy := jobs.DefaultPolicy()
	store := jobs.NewJobStore(pool, policy, nil)
	ctx := context.Background()

	in := ingestInput("invalid output case")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// The budget includes the current failed attempt: two invalid responses
	// must terminate without making a third provider request.
	for i := 0; i < policy.InvalidBudget; i++ {
		claimed := claimDue(t, store, pool)
		if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapInvalidOutputError(errors.New("bad json"))); err != nil {
			t.Fatalf("invalid-output finalize %d: %v", i, err)
		}
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "errored" {
		t.Fatalf("invalid output must terminate after the small budget: %q", job.Status)
	}
	if int(job.AttemptCount) != policy.InvalidBudget {
		t.Fatalf("attempt count = %d, want %d", job.AttemptCount, policy.InvalidBudget)
	}
	if countOutbox(t, pool, job.ID, job.RunGeneration) != 1 {
		t.Fatalf("terminal errored job must enqueue its completion event")
	}
}

func TestConfigurationErrorPausesWithoutBurningBudget(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("config error case")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")
	if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapConfigurationError(errors.New("bad key"))); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "retry_wait" {
		t.Fatalf("status = %q, want retry_wait", job.Status)
	}
	if job.ErrorClass == nil || *job.ErrorClass != jobs.ErrorClassConfiguration {
		t.Fatalf("configuration class must be surfaced: %v", job.ErrorClass)
	}
	// A fixed configuration error must never error the job: the policy pauses
	// with MaxDelay instead of burning the terminal budget.
	if int(job.AttemptCount) >= jobs.DefaultPolicy().MaxAttempts {
		t.Fatalf("configuration errors must not burn the whole budget: %d", job.AttemptCount)
	}
}

func TestClaimWithoutConfigurationQueuesWithoutAttempts(t *testing.T) {
	pool := newTestPool(t) // truncated: config state exists (as at startup), no active version
	if err := config.NewStore(pool).EnsureConfigState(context.Background()); err != nil {
		t.Fatalf("ensure config state: %v", err)
	}
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("queued while unconfigured")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest must work without configuration: %v", err)
	}
	outcome, claimed, err := store.Claim(ctx, "worker-a", 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if outcome != jobs.ClaimNoConfiguration || claimed != nil {
		t.Fatalf("outcome = %v claimed = %v; want no-configuration", outcome, claimed)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "queued" || job.AttemptCount != 0 || job.ConfigVersionID != nil {
		t.Fatalf("unconfigured claim must leave the job queued and untouched: %+v", job)
	}
	if n := countWhere(t, pool,
		`SELECT count(*) FROM moderation_attempts WHERE job_id = $1`, job.ID); n != 0 {
		t.Fatalf("unconfigured claim must not burn an attempt: %d rows", n)
	}
}

func TestRetryThenSucceedCompletesCleanly(t *testing.T) {
	// Regression for the completed_decision_is_present violation: a job that
	// failed once (error_class set by ScheduleJobRetry) must complete after a
	// successful retry — CompleteJob clears error_class and next_attempt_at.
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("retry then succeed")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")
	if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapConnectionError(errors.New("transient"))); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "retry_wait" || job.ErrorClass == nil {
		t.Fatalf("precondition: status=%q errorClass=%v", job.Status, job.ErrorClass)
	}

	claimed = claimDue(t, store, pool)
	if err := store.Complete(ctx, *claimed, jobs.Assessment{
		Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1",
	}); err != nil {
		t.Fatalf("complete after retry: %v", err)
	}
	job = jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "completed" || job.Outcome == nil || *job.Outcome != "approved" {
		t.Fatalf("retry-then-succeed did not complete: %+v", job)
	}
	if job.ErrorClass != nil {
		t.Fatalf("completed job must carry no error_class: %v", *job.ErrorClass)
	}
	if job.NextAttemptAt != nil {
		t.Fatalf("completed job must carry no next_attempt_at: %v", job.NextAttemptAt)
	}
	if countOutbox(t, pool, job.ID, job.RunGeneration) != 1 {
		t.Fatalf("terminal completion must enqueue exactly one outbox event")
	}
}

func TestRejectedAssessmentPublishesRejectedOutcome(t *testing.T) {
	// Regression for the rejected-outcome mapping: a rejected assessment must
	// publish MODERATION_OUTCOME_TYPE_REJECTED with its reason codes — user-
	// service drops an ERROR event carrying content reasons, so mapping a
	// rejection to ERROR silently discarded every rejection.
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("this is harassment")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")
	const policySpecificReason = "REVIEW_TEXT_POLICY_REASON"
	if err := store.Complete(ctx, *claimed, jobs.Assessment{
		Outcome:       jobs.OutcomeRejected,
		ReasonCodes:   []string{policySpecificReason},
		PolicyVersion: "review-text-v1",
	}); err != nil {
		t.Fatalf("complete rejected: %v", err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "completed" || job.Outcome == nil || *job.Outcome != "rejected" {
		t.Fatalf("job = %+v, want completed/rejected", job)
	}
	if len(job.ReasonCodes) != 1 || job.ReasonCodes[0] != policySpecificReason {
		t.Fatalf("reason codes = %v, want [%s]", job.ReasonCodes, policySpecificReason)
	}

	// The published event payload must carry REJECTED + the codes.
	var payload string
	if err := pool.QueryRow(ctx,
		`SELECT payload FROM moderation_outbox WHERE job_id = $1`, job.ID).Scan(&payload); err != nil {
		t.Fatalf("load payload: %v", err)
	}
	if !strings.Contains(payload, "MODERATION_OUTCOME_TYPE_REJECTED") {
		t.Fatalf("payload outcome is not REJECTED: %s", payload)
	}
	if !strings.Contains(payload, policySpecificReason) {
		t.Fatalf("payload missing rejection reason code: %s", payload)
	}
}

func TestStaleFinalizeCannotWriteOutboxOrSchedule(t *testing.T) {
	// Regression for the unfenced terminal finalize: a stale worker whose
	// claim was reclaimed must not schedule a retry or enqueue the terminal
	// outbox row — the phantom row would block the rightful owner's terminal
	// write on UNIQUE(job_id, run_generation) forever.
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("stale finalize target")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	stale := mustClaim(t, store, "stale-worker")

	// The winner reclaims (simulating lease-expiry recovery): steal the claim
	// the way a new claim would.
	jobID := jobIDByRequest(t, pool, in.ModerationRequestID)
	winnerToken := uuid.New()
	if _, err := pool.Exec(ctx, `
		UPDATE moderation_jobs
		SET claim_token = $1, worker_id = 'winner', lease_until = now() + interval '30 s'
		WHERE id = $2`, winnerToken, jobID); err != nil {
		t.Fatalf("steal claim: %v", err)
	}

	// Stale worker reaches the terminal branch: must be fenced, and the outbox
	// must stay empty.
	err := store.FinalizeFailed(ctx, *stale, jobs.WrapConnectionError(errors.New("late failure")))
	if !errors.Is(err, jobs.ErrFenced) {
		t.Fatalf("stale terminal finalize error = %v, want ErrFenced", err)
	}
	if n := countOutbox(t, pool, jobID, 1); n != 0 {
		t.Fatalf("stale worker enqueued %d phantom outbox rows", n)
	}

	// Stale worker reaching the retry branch: fenced too, job untouched.
	staleRetry := *stale
	if err := store.FinalizeFailed(
		ctx,
		staleRetry,
		jobs.WrapConnectionError(errors.New("late failure")),
	); !errors.Is(
		err,
		jobs.ErrFenced,
	) {
		t.Fatalf("stale retry finalize error = %v, want ErrFenced", err)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM moderation_jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("load job: %v", err)
	}
	if status != "processing" {
		t.Fatalf("stale finalize mutated the job: status=%q", status)
	}
}

func TestReleaseRequeuesForPromptRetry(t *testing.T) {
	// Regression for the broken Release: releasing a claimed job must requeue
	// it (queued, claim cleared) instead of failing the processing_is_claimed
	// CHECK and silently waiting out the lease.
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()

	in := ingestInput("release me")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "shutdown-worker")
	jobID := jobIDByRequest(t, pool, in.ModerationRequestID)

	released, err := store.Release(ctx, jobID, claimed.ClaimToken)
	if err != nil || !released {
		t.Fatalf("release: released=%v err=%v", released, err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "queued" {
		t.Fatalf("released job status = %q, want queued", job.Status)
	}
	if job.ClaimToken != nil || job.LeaseUntil != nil {
		t.Fatalf("released job must carry no claim: %+v", job)
	}
	// And it must be immediately claimable again.
	outcome, again, err := store.Claim(ctx, "next-worker", 30*time.Second)
	if err != nil || outcome != jobs.ClaimReady || again == nil || again.Job.ID != jobID {
		t.Fatalf("released job not reclaimable: %v %v %v", outcome, again, err)
	}
}

func TestReleasedJobReclaimsAfterShutdownContextCancel(t *testing.T) {
	// The shutdown path releases with an already-cancelled context; the pool
	// must detach the context so the write still lands and the job is
	// promptly requeued instead of waiting out the lease.
	pool := newTestPool(t)
	activateTestConfig(t, pool)

	in := ingestInput("cancel then release")
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	if _, err := store.Ingest(context.Background(), in); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	eval := newBlockingEvaluator()
	p := jobs.NewPool(jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil), eval, jobs.NewLimiter(1), jobs.PoolConfig{
		Concurrency:       1,
		Lease:             30 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		PollInterval:      10 * time.Millisecond,
		LLMRequestTimeout: 10 * time.Second,
		Policy:            jobs.DefaultPolicy(),
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	p.Start(ctx)

	// Worker is inside the (blocking) evaluation; cancel to trigger shutdown.
	eval.waitEntered(t, 1)
	cancel()
	eval.release(1)
	p.Wait()

	var status string
	if err := pool.QueryRow(context.Background(),
		`SELECT status FROM moderation_jobs WHERE moderation_request_id = $1`,
		in.ModerationRequestID).Scan(&status); err != nil {
		t.Fatalf("load job: %v", err)
	}
	if status != "queued" {
		t.Fatalf("status = %q after shutdown release, want queued", status)
	}
}

func TestMigrationUpDownUp(t *testing.T) {
	url := envOrSkip(t)
	ctx := context.Background()
	if err := database.MigrateDownAll(ctx, url); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatalf("up: %v", err)
	}
	if err := database.MigrateDownAll(ctx, url); err != nil {
		t.Fatalf("down again: %v", err)
	}
	if err := database.Migrate(ctx, url); err != nil {
		t.Fatalf("up again: %v", err)
	}
}

func TestAdminAuditRejectsUnknownAction(t *testing.T) {
	pool := newTestPool(t)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO moderation_admin_audit (action, actor)
		VALUES ('UNKNOWN_ACTION', 'tester')`); err == nil {
		t.Fatal("expected unknown audit action to violate the database constraint")
	}
}

func TestAutomaticRetryRetainsPinnedConfiguration(t *testing.T) {
	pool := newTestPool(t)
	first := activateTestConfig(t, pool)
	store := jobs.NewJobStore(pool, jobs.PolicyFromConfig(2, time.Millisecond, time.Millisecond), nil)
	ctx := context.Background()
	in := ingestInput("pin my model")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	claimed := claimDue(t, store, pool)
	if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapServerError(errors.New("temporary"))); err != nil {
		t.Fatal(err)
	}
	configStore := config.NewStore(pool)
	second, err := configStore.InsertConfigVersion(
		ctx,
		"different-model",
		[]byte(`{}`),
		config.OutputModeJSONObject,
		"review-text-v1",
		"other",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := configStore.ActivateConfigVersion(ctx, second, "admin", &first); err != nil {
		t.Fatal(err)
	}
	claimed = claimDue(t, store, pool)
	if claimed.Config.VersionID != first || *claimed.Job.ConfigVersionID != first.String() {
		t.Fatalf("automatic retry changed configuration: %+v", claimed.Config)
	}
	if err := store.FinalizeFailed(ctx, *claimed, jobs.WrapServerError(errors.New("temporary"))); err != nil {
		t.Fatal(err)
	}
	if job := jobByRequest(t, pool, in.ModerationRequestID); job.Status != "errored" || job.AttemptCount != 2 {
		t.Fatalf("configured retry budget ignored: %+v", job)
	}
}

func TestConcurrentIngestionVerifiesImmutableSnapshot(t *testing.T) {
	pool := newTestPool(t)
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	ctx := context.Background()
	in := ingestInput("original immutable text")
	errorsC := make(chan error, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, err := store.Ingest(ctx, in); errorsC <- err })
	}
	wg.Wait()
	close(errorsC)
	for err := range errorsC {
		if err != nil {
			t.Fatalf("concurrent ingestion: %v", err)
		}
	}
	if got := countJobs(t, pool); got != 1 {
		t.Fatalf("job count = %d, want 1", got)
	}
	conflict := in
	conflict.Comment = "changed text"
	if result, err := store.Ingest(ctx, conflict); err != nil || result != jobs.IngestionConflictQuarantined {
		t.Fatalf("changed snapshot = %v, %v", result, err)
	}
	conflict = in
	conflict.ModerationRequestID = uuid.New()
	if result, err := store.Ingest(ctx, conflict); err != nil || result != jobs.IngestionConflictQuarantined {
		t.Fatalf("reused source event = %v, %v", result, err)
	}
}

func TestLeaseUsesDatabaseClockAndRejectsExpiredOwner(t *testing.T) {
	pool := newTestPool(t)
	activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	in := ingestInput("clock fencing")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	claimed := mustClaim(t, store, "worker")
	// A skewed process supplies the duration, while PostgreSQL owns the clock.
	skewed := time.Now().Add(-24 * time.Hour)
	if ok, err := store.Heartbeat(
		ctx,
		claimed.Job.ID,
		claimed.ClaimToken,
		skewed,
		skewed.Add(time.Minute),
	); err != nil ||
		!ok {
		t.Fatalf("heartbeat = %v, %v", ok, err)
	}
	var live bool
	if err := pool.QueryRow(ctx, `SELECT lease_until > now() + interval '30 seconds' FROM moderation_jobs WHERE id = $1`, claimed.Job.ID).
		Scan(&live); err != nil ||
		!live {
		t.Fatalf("lease followed process clock: %v, %v", live, err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE moderation_jobs SET lease_until = now() - interval '1s' WHERE id = $1`,
		claimed.Job.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.Complete(
		ctx,
		*claimed,
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); !errors.Is(
		err,
		jobs.ErrFenced,
	) {
		t.Fatalf("expired owner completed: %v", err)
	}
	if ok, err := store.Heartbeat(
		ctx,
		claimed.Job.ID,
		claimed.ClaimToken,
		time.Now(),
		time.Now().Add(time.Minute),
	); err != nil ||
		ok {
		t.Fatalf("expired owner renewed: %v, %v", ok, err)
	}
}

func TestConfigurationPauseSurvivesRestartAndRequiresValidatedAdminRecovery(t *testing.T) {
	pool := newTestPool(t)
	first := activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	input := ingestInput("private review")
	if _, err := store.Ingest(ctx, input); err != nil {
		t.Fatal(err)
	}
	claimed := mustClaim(t, store, "worker")
	if err := store.FinalizeFailed(
		ctx,
		*claimed,
		jobs.WrapConfigurationError(errors.New("sk-private-secret private-review")),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(
		ctx,
		`UPDATE moderation_jobs SET next_attempt_at=now() - interval '1 day' WHERE moderation_request_id=$1`,
		input.ModerationRequestID,
	); err != nil {
		t.Fatal(err)
	}
	// No wall-clock backoff or process restart can retry an unchanged broken config.
	for range 3 {
		store = jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
		if outcome, _, err := store.Claim(ctx, "restarted", time.Minute); err != nil || outcome != jobs.ClaimNone {
			t.Fatalf("paused configuration claimed again: %v, %v", outcome, err)
		}
	}
	job := jobByRequest(t, pool, input.ModerationRequestID)
	if job.AttemptCount != 1 {
		t.Fatalf("paused job consumed attempts: %d", job.AttemptCount)
	}
	summary, err := review.NewStore(pool).GetJobByID(ctx, job.ID)
	if err != nil || !summary.GetConfigurationPaused() {
		t.Fatalf("pause not exposed to owner: %+v, %v", summary, err)
	}

	var detail string
	if err := pool.QueryRow(ctx, `SELECT detail FROM moderation_attempts WHERE job_id=$1`, job.ID).
		Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail, "private") {
		t.Fatalf("unsafe persisted diagnostic: %q", detail)
	}

	configs := config.NewStore(pool)
	second, err := configs.InsertConfigVersion(
		ctx,
		"corrected",
		[]byte(`{}`),
		config.OutputModeJSONObject,
		"review-text-v1",
		"test",
		"admin",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := configs.ActivateAndResume(ctx, second, "admin", &first, false); err != nil {
		t.Fatal(err)
	}
	// Ordinary activation does not silently rebind old pinned jobs.
	if outcome, _, err := store.Claim(ctx, "worker", time.Minute); err != nil || outcome != jobs.ClaimNone {
		t.Fatalf("old configuration unexpectedly resumed: %v, %v", outcome, err)
	}
	fresh := ingestInput("fresh work uses new configuration")
	if _, err := store.Ingest(ctx, fresh); err != nil {
		t.Fatal(err)
	}
	ready := mustClaim(t, store, "worker")
	if ready.Job.ModerationRequestID != fresh.ModerationRequestID || ready.Config.VersionID != second {
		t.Fatal("paused work starved new configuration")
	}
	if err := store.Complete(
		ctx,
		*ready,
		jobs.Assessment{Outcome: jobs.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); err != nil {
		t.Fatal(err)
	}
	if err := configs.ActivateAndResume(
		ctx,
		second,
		"admin",
		&first,
		true,
	); !errors.Is(
		err,
		config.ErrConfigurationConflict,
	) {
		t.Fatalf("stale activation resumed work: %v", err)
	}
	if err := configs.ActivateAndResume(ctx, second, "admin", &second, true); err != nil {
		t.Fatal(err)
	}
	recovered := mustClaim(t, store, "worker")
	if recovered.Job.ID != job.ID || recovered.Job.RunGeneration != 2 || recovered.AttemptNumber != 1 ||
		recovered.Config.VersionID != second {
		t.Fatalf("explicit recovery = %+v", recovered)
	}
	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM moderation_admin_audit WHERE action='RESUME_CONFIGURATION_PAUSED_JOB' AND job_id=$1`, job.ID).
		Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 1 {
		t.Fatalf("recovery audit count = %d", audits)
	}
}

func TestRevalidatingSameConfigurationResumesPinnedJob(t *testing.T) {
	pool := newTestPool(t)
	version := activateTestConfig(t, pool)
	ctx := context.Background()
	store := jobs.NewJobStore(pool, jobs.DefaultPolicy(), nil)
	in := ingestInput("credentials rotated")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatal(err)
	}
	claimed := mustClaim(t, store, "worker")
	if err := store.FinalizeFailed(
		ctx,
		*claimed,
		jobs.WrapConfigurationError(errors.New("bad credential")),
	); err != nil {
		t.Fatal(err)
	}
	if err := config.NewStore(pool).ActivateConfigVersion(ctx, version, "admin", &version); err != nil {
		t.Fatal(err)
	}
	recovered := mustClaim(t, store, "worker")
	if recovered.Config.VersionID != version || recovered.Job.RunGeneration != 1 || recovered.AttemptNumber != 2 {
		t.Fatalf("same-config revalidation = %+v", recovered)
	}
}
