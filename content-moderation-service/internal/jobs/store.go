// Package jobs implements the durable review-moderation engine. This file owns
// persistence: ingestion deduplication, replica-safe claims, heartbeats, the
// fenced terminal transition and the read model the review RPC consumes.
package jobs

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrFenced is returned when a terminal write finds a stale claim: another
// worker already completed or scheduled the job. The job is left for the lease
// machinery or the winning worker; this worker simply returns to its pool.
var ErrFenced = errors.New("moderation job claim is stale")

// UsersReviewSubmittedTopic is the topic this service consumes for review
// submissions. It is exported so the composition root can register it with the
// Kafka subscriber.
const UsersReviewSubmittedTopic = "users.review_submitted"

// JobStore is the name external packages (the composition root, the Kafka
// ingestion handler) reference for the moderation job persistence owner. It is
// an alias of the concrete *jobStore; the call-only method set defines the
// boundary, so the unexported fields and internal writes stay inside the
// package.
type JobStore = jobStore

// jobStore binds the engine's queries to one connection pool. Writes run inside
// database.Within; reads run directly against the pool.
type jobStore struct {
	pool   *pgxpool.Pool
	policy Policy
	// rngMu guards rng: one jobStore is shared by every pool worker, and
	// math/rand.Rand is not safe for concurrent use.
	rngMu sync.Mutex
	rng   *rand.Rand
}

// claimOutcome reports what a claim cycle produced.
type claimOutcome int

const (
	ClaimNone claimOutcome = iota
	ClaimNoConfiguration
	ClaimReady
)

// ClaimedJob is a job a worker owns until it completes or is scheduled again.
type ClaimedJob struct {
	Job           moderationdb.ModerationJob
	ClaimToken    uuid.UUID
	AttemptNumber int32
	Config        moderationdb.ModerationConfigVersion
}

// NewJobStore constructs the persistence owner for one connection pool.
func NewJobStore(pool *pgxpool.Pool, policy Policy, rng *rand.Rand) *jobStore {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &jobStore{pool: pool, policy: policy, rng: rng}
}

// Heartbeat extends the claim's lease while an assessment runs. The boolean is
// false when the claim vanished (lost lease), so the worker can cancel.
func (s *jobStore) Heartbeat(
	ctx context.Context,
	jobID uuid.UUID,
	claimToken uuid.UUID,
	heartbeatAt, leaseUntil time.Time,
) (bool, error) {
	q := moderationdb.New(s.pool)
	rows, err := q.HeartbeatJob(ctx, moderationdb.HeartbeatJobParams{
		HeartbeatAt: &heartbeatAt,
		LeaseUntil:  &leaseUntil,
		ID:          jobID,
		ClaimToken:  &claimToken,
	})
	if err != nil {
		return false, err
	}
	return rows > 0, nil
}

// Release returns owned work on shutdown, requeueing it so a later cycle
// retries promptly. The boolean is false when the claim was already gone.
func (s *jobStore) Release(ctx context.Context, jobID uuid.UUID, claimToken uuid.UUID) (bool, error) {
	q := moderationdb.New(s.pool)
	rows, err := q.ReleaseJob(ctx, moderationdb.ReleaseJobParams{ID: jobID, ClaimToken: &claimToken})
	return rows > 0, err
}

// Claim performs the short claim transaction: lock the oldest eligible job, pin
// its active configuration before first inference, mark it processing, and
// record the started attempt. The claim lock is released by the transaction, so
// no lock is held during inference.
func (s *jobStore) Claim(ctx context.Context, workerID string, lease time.Duration) (claimOutcome, *ClaimedJob, error) {
	var outcome claimOutcome
	var claimed *ClaimedJob

	err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		job, err := q.ClaimEligibleJob(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			outcome = ClaimNone
			return nil
		}
		if err != nil {
			return err
		}

		active, err := q.GetActiveConfigVersion(ctx)
		if err != nil {
			return err
		}
		if active == nil {
			// No configuration yet: leave the job queued and try again later.
			outcome = ClaimNoConfiguration
			return nil
		}

		config, err := q.GetActiveConfigVersionFull(ctx)
		if err != nil {
			return err
		}

		configVersionID := config.VersionID.String()
		policyVersion := config.PolicyVersion
		if job.ConfigVersionID == nil {
			if err := q.PinActiveConfig(ctx, moderationdb.PinActiveConfigParams{
				ConfigVersionID: &configVersionID,
				PolicyVersion:   &policyVersion,
				ID:              job.ID,
			}); err != nil {
				return err
			}
		}
		job.ConfigVersionID = &configVersionID
		job.PolicyVersion = &policyVersion

		claimToken := uuid.New()
		leaseUntil := time.Now().Add(lease)
		attemptNumber := job.AttemptCount + 1
		modelID := config.ModelID

		if err := q.ClaimJob(ctx, moderationdb.ClaimJobParams{
			ClaimToken: &claimToken,
			WorkerID:   &workerID,
			LeaseUntil: &leaseUntil,
			ID:         job.ID,
		}); err != nil {
			return err
		}
		if err := q.InsertAttempt(ctx, moderationdb.InsertAttemptParams{
			JobID:           job.ID,
			RunGeneration:   job.RunGeneration,
			AttemptNumber:   attemptNumber,
			ConfigVersionID: &configVersionID,
			ModelID:         &modelID,
		}); err != nil {
			return err
		}

		outcome = ClaimReady
		claimed = &ClaimedJob{Job: job, ClaimToken: claimToken, AttemptNumber: attemptNumber, Config: config}
		return nil
	})
	if err != nil {
		return ClaimNone, nil, err
	}
	return outcome, claimed, nil
}

// Complete runs a valid assessment to a terminal completed outcome, finishing
// the attempt and enqueuing the outbox event atomically, fenced by claim token.
func (s *jobStore) Complete(ctx context.Context, c ClaimedJob, assessment Assessment) error {
	reasonCodes := reasonCodesFor(assessment.Outcome, assessment.ReasonCodes)
	outcomeStr := string(assessment.Outcome)
	finishedAt := time.Now()
	eventID := uuid.New()
	policyVersion := assessment.PolicyVersion

	payload, err := buildOutboxPayload(eventID, c.Job,
		outcomeTypeFor(assessment.Outcome), reasonCodes, finishedAt)
	if err != nil {
		return err
	}

	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		rows, err := q.CompleteJob(ctx, moderationdb.CompleteJobParams{
			ID:            c.Job.ID,
			RunGeneration: c.Job.RunGeneration,
			ClaimToken:    &c.ClaimToken,
			Outcome:       &outcomeStr,
			ReasonCodes:   reasonCodes,
			PolicyVersion: &policyVersion,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrFenced
		}
		if err := q.FinishAttempt(ctx, moderationdb.FinishAttemptParams{
			JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration, AttemptNumber: c.AttemptNumber,
			Outcome: "completed", ErrorClass: nil, Detail: nil,
		}); err != nil {
			return err
		}
		return q.InsertOutbox(ctx, moderationdb.InsertOutboxParams{
			EventID: eventID, JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration,
			Payload: payload, AvailableAt: finishedAt,
		})
	})
}

// FinalizeFailed records a failed attempt and either schedules a bounded retry
// or errors the job terminally, enqueuing the outbox event only on terminal
// failure.
func (s *jobStore) FinalizeFailed(ctx context.Context, c ClaimedJob, evalErr error) error {
	errorClass, kind, retryAfter := Classify(evalErr)
	finishedAt := time.Now()
	diagnostic := maskDiag(evalErr.Error())
	eventID := uuid.New()

	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		invalidClass := ErrorClassInvalidOutput
		invalidCount, err := q.CountAttemptsByClass(ctx, moderationdb.CountAttemptsByClassParams{
			JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration, ErrorClass: &invalidClass,
		})
		if err != nil {
			return err
		}
		filterClass := ErrorClassContentFilter
		filterCount, err := q.CountAttemptsByClass(ctx, moderationdb.CountAttemptsByClassParams{
			JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration, ErrorClass: &filterClass,
		})
		if err != nil {
			return err
		}

		// The shared generator must be mutex-guarded: workers finalize failures
		// concurrently and math/rand.Rand is not goroutine-safe.
		s.rngMu.Lock()
		decision := s.policy.decide(kind, int(c.AttemptNumber), int(invalidCount), int(filterCount), s.rng)
		s.rngMu.Unlock()
		wait := decision.wait
		if kind == kindDurable && retryAfter > 0 {
			wait = retryAfter
			if wait > s.policy.MaxDelay {
				wait = s.policy.MaxDelay
			}
		}

		errorClassPtr := errorClass
		if err := q.FinishAttempt(ctx, moderationdb.FinishAttemptParams{
			JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration, AttemptNumber: c.AttemptNumber,
			Outcome: "", ErrorClass: &errorClassPtr, Detail: &diagnostic,
		}); err != nil {
			return err
		}

		if decision.retry {
			nextAttemptAt := finishedAt.Add(wait)
			rows, err := q.ScheduleJobRetry(ctx, moderationdb.ScheduleJobRetryParams{
				ID: c.Job.ID, RunGeneration: c.Job.RunGeneration, ClaimToken: &c.ClaimToken,
				ErrorClass: &errorClassPtr, NextAttemptAt: &nextAttemptAt,
			})
			if err != nil {
				return err
			}
			if rows == 0 {
				// The claim was lost (another worker reclaimed the job); this
				// attempt's failure was recorded but the schedule write belongs
				// to the current owner.
				return ErrFenced
			}
			return nil
		}

		payload, err := buildOutboxPayload(eventID, c.Job, moderationoutcomeTypeError(), nil, finishedAt)
		if err != nil {
			return err
		}
		rows, err := q.ErrorJob(ctx, moderationdb.ErrorJobParams{
			ID: c.Job.ID, RunGeneration: c.Job.RunGeneration, ClaimToken: &c.ClaimToken, ErrorClass: &errorClassPtr,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			// Fenced out: never enqueue the terminal event for a generation a
			// stale claim no longer owns — the outbox row would block the
			// rightful owner's terminal write on the unique constraint.
			return ErrFenced
		}
		return q.InsertOutbox(ctx, moderationdb.InsertOutboxParams{
			EventID: eventID, JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration,
			Payload: payload, AvailableAt: finishedAt,
		})
	})
}

// GetJobByID reads a single job by primary key.
func (s *jobStore) GetJobByID(ctx context.Context, id uuid.UUID) (moderationdb.ModerationJob, error) {
	return moderationdb.New(s.pool).GetJobByID(ctx, id)
}

// GetJobsForReviewer reads owner-visible summaries for a reviewer's jobs.
func (s *jobStore) GetJobsForReviewer(
	ctx context.Context,
	reviewerID uuid.UUID,
	moderationRequestIDs []uuid.UUID,
) ([]moderationdb.ModerationJob, error) {
	return moderationdb.New(s.pool).GetJobsForReviewer(ctx, moderationdb.GetJobsForReviewerParams{
		ReviewerID:           reviewerID,
		ModerationRequestIds: moderationRequestIDs,
	})
}

// GetAttempts reads an attempt's history for one generation, oldest first.
func (s *jobStore) GetAttempts(
	ctx context.Context,
	jobID uuid.UUID,
	runGeneration int32,
) ([]moderationdb.ModerationAttempt, error) {
	return moderationdb.New(s.pool).GetAttempts(ctx, moderationdb.GetAttemptsParams{
		JobID: jobID, RunGeneration: runGeneration,
	})
}

// validateAssessment enforces the contract between outcome and reason codes so
// an invalid evaluator output is treated as invalid output (retryable).
func validateAssessment(a Assessment) error {
	switch a.Outcome {
	case OutcomeApproved:
		if len(a.ReasonCodes) > 0 {
			return WrapInvalidOutputError(errors.New("approved assessment must carry no reason codes"))
		}
		if a.PolicyVersion == "" {
			return WrapInvalidOutputError(errors.New("assessment missing policy version"))
		}
	case OutcomeRejected:
		if len(a.ReasonCodes) == 0 {
			return WrapInvalidOutputError(errors.New("rejected assessment must carry at least one reason code"))
		}
		for _, code := range a.ReasonCodes {
			if !isKnownReason(code) {
				return WrapInvalidOutputError(errors.New("unknown reason code " + code))
			}
		}
		if a.PolicyVersion == "" {
			return WrapInvalidOutputError(errors.New("assessment missing policy version"))
		}
	default:
		return WrapInvalidOutputError(errors.New("assessment outcome is not approved or rejected"))
	}
	return nil
}

func isKnownReason(code string) bool {
	switch code {
	case ReasonHarassment, ReasonHate, ReasonThreat, ReasonSexualContent, ReasonSpam:
		return true
	default:
		return false
	}
}
