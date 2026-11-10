//go:build integration

// Integration tests for the durable moderation engine against a real
// PostgreSQL. Set TEST_DATABASE_URL to a throwaway database; the tests apply
// the module migrations and truncate all tables between subtests.
package job_test

import (
	"context"
	"errors"
	"fmt"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
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
			moderation_quarantine, moderation_admin_audit`)
	if err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

func ingestInput(comment string) moderationjob.IngestionInput {
	return moderationjob.IngestionInput{
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
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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

func mustClaim(t *testing.T, store *moderationjob.Service, workerID string) *moderationjob.ClaimedJob {
	t.Helper()
	outcome, claimed, err := store.Claim(context.Background(), workerID, 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if outcome != moderationjob.ClaimReady {
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
func claimDue(t *testing.T, store *moderationjob.Service, pool *pgxpool.Pool) *moderationjob.ClaimedJob {
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
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
	ctx := context.Background()

	t.Run("first delivery inserts one queued job", func(t *testing.T) {
		in := ingestInput("a calm review")
		result, err := store.Ingest(ctx, in)
		if err != nil {
			t.Fatalf("ingest: %v", err)
		}
		if result != moderationjob.IngestionInserted {
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
		if result != moderationjob.IngestionDeduplicated {
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
		if result != moderationjob.IngestionConflictQuarantined {
			t.Fatalf("result = %v, want conflict_quarantined", result)
		}
		if countQuarantined(t, pool) == 0 {
			t.Fatalf("conflict was not quarantined")
		}
	})

	t.Run("malformed event is quarantined and acknowledged", func(t *testing.T) {
		before := countQuarantined(t, pool)
		err := store.QuarantineMalformed(ctx, moderationjob.MalformedInput{
			Topic: moderationjob.UsersReviewSubmittedTopic, MessageKey: "not-a-uuid",
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

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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
		if outcome != moderationjob.ClaimReady || claimed == nil {
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

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
	ctx := context.Background()

	in := ingestInput("fencing target")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")

	// A stale worker (wrong claim token) must not complete the job.
	stale := *claimed
	stale.ClaimToken = uuid.New()
	err := store.Complete(ctx, stale, moderation.ReviewAssessment{
		Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1",
	})
	if !errors.Is(err, moderationjob.ErrFenced) {
		t.Fatalf("stale complete error = %v, want ErrFenced", err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "processing" || job.Outcome != nil {
		t.Fatalf("stale completion mutated the job: %+v", job)
	}

	// The rightful owner completes afterwards.
	if err := store.Complete(ctx, *claimed, moderation.ReviewAssessment{
		Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1",
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

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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
	if err != nil || outcome != moderationjob.ClaimReady || recovered == nil {
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

	policy := moderationjob.DefaultRetrySettings()
	store := moderationjob.NewService(pool, policy, nil)
	ctx := context.Background()

	in := ingestInput("doomed review")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")

	// A durable failure inside the budget schedules a delayed retry. The wait
	// is fully jittered in [0, initial] for the first attempt, so only the
	// bounds are asserted.
	if err := store.FinalizeFailed(ctx, *claimed, moderation.WrapConnectionError(errors.New("boom"))); err != nil {
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
		if err := store.FinalizeFailed(ctx, *claimed, moderation.WrapConnectionError(errors.New("boom"))); err != nil {
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
	if job.ErrorClass == nil || *job.ErrorClass != moderation.ErrorClassConnection {
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
	if outcome == moderationjob.ClaimReady && claimed2 != nil && claimed2.Job.ID == job.ID {
		t.Fatalf("terminal job was claimed again")
	}
}

func TestInvalidOutputBudgetEndsInError(t *testing.T) {
	pool := newTestPool(t)

	policy := moderationjob.DefaultRetrySettings()
	store := moderationjob.NewService(pool, policy, nil)
	ctx := context.Background()

	in := ingestInput("invalid output case")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	// The budget includes the current failed attempt: two invalid responses
	// must terminate without making a third provider request.
	for i := 0; i < policy.InvalidBudget; i++ {
		claimed := claimDue(t, store, pool)
		if err := store.FinalizeFailed(
			ctx,
			*claimed,
			moderation.WrapInvalidOutputError(errors.New("bad json")),
		); err != nil {
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

func TestRetryThenSucceedCompletesCleanly(t *testing.T) {
	// Regression for the completed_decision_is_present violation: a job that
	// failed once (error_class set by ScheduleJobRetry) must complete after a
	// successful retry — CompleteJob clears error_class and next_attempt_at.
	pool := newTestPool(t)

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
	ctx := context.Background()

	in := ingestInput("retry then succeed")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")
	if err := store.FinalizeFailed(ctx, *claimed, moderation.WrapConnectionError(errors.New("transient"))); err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	job := jobByRequest(t, pool, in.ModerationRequestID)
	if job.Status != "retry_wait" || job.ErrorClass == nil {
		t.Fatalf("precondition: status=%q errorClass=%v", job.Status, job.ErrorClass)
	}

	claimed = claimDue(t, store, pool)
	if err := store.Complete(ctx, *claimed, moderation.ReviewAssessment{
		Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1",
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

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
	ctx := context.Background()

	in := ingestInput("this is harassment")
	if _, err := store.Ingest(ctx, in); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	claimed := mustClaim(t, store, "worker-a")
	const policySpecificReason = "REVIEW_TEXT_POLICY_REASON"
	if err := store.Complete(ctx, *claimed, moderation.ReviewAssessment{
		Outcome:       moderation.OutcomeRejected,
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

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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
	err := store.FinalizeFailed(ctx, *stale, moderation.WrapConnectionError(errors.New("late failure")))
	if !errors.Is(err, moderationjob.ErrFenced) {
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
		moderation.WrapConnectionError(errors.New("late failure")),
	); !errors.Is(
		err,
		moderationjob.ErrFenced,
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

	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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
	if err != nil || outcome != moderationjob.ClaimReady || again == nil || again.Job.ID != jobID {
		t.Fatalf("released job not reclaimable: %v %v %v", outcome, again, err)
	}
}

func TestReleasedJobReclaimsAfterShutdownContextCancel(t *testing.T) {
	// The shutdown path releases with an already-cancelled context; the pool
	// must detach the context so the write still lands and the job is
	// promptly requeued instead of waiting out the lease.
	pool := newTestPool(t)

	in := ingestInput("cancel then release")
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
	if _, err := store.Ingest(context.Background(), in); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	eval := newBlockingEvaluator()
	p := moderationjob.NewPool(
		moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil),
		eval,
		moderation.NewInferenceLimiter(1),
		moderationjob.PoolConfig{
			Concurrency:       1,
			Lease:             30 * time.Second,
			HeartbeatInterval: 5 * time.Second,
			PollInterval:      10 * time.Millisecond,
			LLMRequestTimeout: 10 * time.Second,
			RetrySettings:     moderationjob.DefaultRetrySettings(),
		},
		nil,
	)
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

func TestConcurrentIngestionVerifiesImmutableSnapshot(t *testing.T) {
	pool := newTestPool(t)
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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
	if result, err := store.Ingest(ctx, conflict); err != nil || result != moderationjob.IngestionConflictQuarantined {
		t.Fatalf("changed snapshot = %v, %v", result, err)
	}
	conflict = in
	conflict.ModerationRequestID = uuid.New()
	if result, err := store.Ingest(ctx, conflict); err != nil || result != moderationjob.IngestionConflictQuarantined {
		t.Fatalf("reused source event = %v, %v", result, err)
	}
}

func TestLeaseUsesDatabaseClockAndRejectsExpiredOwner(t *testing.T) {
	pool := newTestPool(t)

	ctx := context.Background()
	store := moderationjob.NewService(pool, moderationjob.DefaultRetrySettings(), nil)
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
		moderation.ReviewAssessment{Outcome: moderation.OutcomeApproved, PolicyVersion: "review-text-v1"},
	); !errors.Is(
		err,
		moderationjob.ErrFenced,
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
