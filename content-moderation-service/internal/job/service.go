package job

import (
	"context"
	"errors"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrFenced discards a stale worker write without changing the winning claim.
var ErrFenced = errors.New("moderation job claim is stale")

const UsersReviewSubmittedTopic = "users.review_submitted"

type Service struct {
	pool    *pgxpool.Pool
	retry   RetrySettings
	modelID string
	// Shared by concurrent workers; rand.Rand requires synchronization.
	rngMu sync.Mutex
	rng   *rand.Rand
}

func NewService(pool *pgxpool.Pool, retry RetrySettings, rng *rand.Rand, modelID string) *Service {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	store := &Service{pool: pool, retry: retry, rng: rng, modelID: modelID}
	return store
}

// False means the claim was lost; cancel the in-flight assessment.
func (s *Service) Heartbeat(
	ctx context.Context,
	jobID uuid.UUID,
	claimToken uuid.UUID,
	heartbeatAt, leaseUntil time.Time,
) (bool, error) {
	var rows int64
	err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		if _, err := q.LockJob(ctx, jobID); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		var err error
		rows, err = q.HeartbeatJob(ctx, moderationdb.HeartbeatJobParams{
			LeaseSeconds: leaseUntil.Sub(heartbeatAt).Seconds(), ID: jobID, ClaimToken: &claimToken,
		})
		return err
	})
	return rows > 0, err
}

// Release requeues owned work; false means the claim is stale.
func (s *Service) Release(ctx context.Context, jobID uuid.UUID, claimToken uuid.UUID) (bool, error) {
	return s.release(ctx, jobID, claimToken, nil)
}

// Record a sanitized configuration failure and release ownership atomically, without a terminal event.
func (s *Service) ReleaseConfigurationFailure(
	ctx context.Context,
	jobID uuid.UUID,
	claimToken uuid.UUID,
) (bool, error) {
	class := moderation.ErrorClassConfiguration
	return s.release(ctx, jobID, claimToken, &class)
}

func (s *Service) release(
	ctx context.Context,
	jobID uuid.UUID,
	claimToken uuid.UUID,
	errorClass *string,
) (bool, error) {
	var released bool
	err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		if _, err := q.LockJob(ctx, jobID); errors.Is(err, pgx.ErrNoRows) {
			return nil
		} else if err != nil {
			return err
		}
		job, err := q.ReleaseJob(ctx, moderationdb.ReleaseJobParams{ID: jobID, ClaimToken: &claimToken})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		outcome := "released"
		if errorClass != nil {
			outcome = ""
		}
		released = true
		return q.FinishAttempt(ctx, moderationdb.FinishAttemptParams{
			JobID: job.ID, RunGeneration: job.RunGeneration, AttemptNumber: job.AttemptCount,
			Outcome: outcome, ErrorClass: errorClass, Detail: errorClass,
		})
	})
	return released, err
}

// Claim commits ownership and attempt history before inference starts.
func (s *Service) Claim(
	ctx context.Context,
	workerID string,
	lease time.Duration,
) (ClaimOutcome, *ClaimedJob, error) {
	var outcome ClaimOutcome
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

		if job.Status == "processing" {
			class := "lease_expired"
			if err := q.FinishAttempt(ctx, moderationdb.FinishAttemptParams{
				JobID: job.ID, RunGeneration: job.RunGeneration, AttemptNumber: job.AttemptCount,
				Outcome: "abandoned", ErrorClass: &class,
			}); err != nil {
				return err
			}
		}
		if int(job.AttemptCount) >= s.retry.MaxAttempts {
			if err := exhaustJob(ctx, q, job); err != nil {
				return err
			}
			outcome = ClaimExhausted
			return nil
		}

		claimToken := uuid.New()
		attemptNumber := job.AttemptCount + 1
		modelID := s.modelID

		if err := q.ClaimJob(ctx, moderationdb.ClaimJobParams{
			ClaimToken:   &claimToken,
			WorkerID:     &workerID,
			LeaseSeconds: lease.Seconds(),
			ID:           job.ID,
		}); err != nil {
			return err
		}
		if err := q.InsertAttempt(ctx, moderationdb.InsertAttemptParams{
			JobID:         job.ID,
			RunGeneration: job.RunGeneration,
			AttemptNumber: attemptNumber,
			ModelID:       &modelID,
		}); err != nil {
			return err
		}

		outcome = ClaimReady
		claimed = &ClaimedJob{
			Job:           jobSnapshot(job),
			ClaimToken:    claimToken,
			AttemptNumber: attemptNumber,
		}
		return nil
	})
	if err != nil {
		return ClaimNone, nil, err
	}
	return outcome, claimed, nil
}

// Atomically commit verdict, attempt and outbox, fenced by generation and claim.
func (s *Service) Complete(ctx context.Context, c ClaimedJob, assessment moderation.ReviewAssessment) error {
	if err := validateAssessment(assessment); err != nil {
		return err
	}
	reasonCodes := reasonCodesFor(assessment.Outcome, assessment.ReasonCodes)
	outcomeStr := string(assessment.Outcome)
	finishedAt := time.Now()
	eventID := uuid.New()
	policyVersion := assessment.PolicyVersion

	c.Job.PolicyVersion = &assessment.PolicyVersion
	payload, err := buildOutboxPayload(eventID, c.Job,
		outcomeTypeFor(assessment.Outcome), reasonCodes, finishedAt)
	if err != nil {
		return err
	}

	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		if _, err := q.LockJob(ctx, c.Job.ID); errors.Is(err, pgx.ErrNoRows) {
			return ErrFenced
		} else if err != nil {
			return err
		}
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
			PayloadVersion: 1, Payload: payload, AvailableAt: finishedAt,
		})
	})
}

// Record the attempt and schedule retry, or commit terminal ERROR and outbox.
func (s *Service) FinalizeFailed(ctx context.Context, c ClaimedJob, evalErr error) error {
	errorClass, kind, retryAfter := moderation.Classify(evalErr)
	finishedAt := time.Now()
	diagnostic := errorClass
	eventID := uuid.New()

	return database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		if _, err := q.LockOwnedJob(ctx, moderationdb.LockOwnedJobParams{
			ID: c.Job.ID, RunGeneration: c.Job.RunGeneration, ClaimToken: &c.ClaimToken,
		}); errors.Is(err, pgx.ErrNoRows) {
			return ErrFenced
		} else if err != nil {
			return err
		}

		classCount := 0
		if kind == moderation.KindInvalidOutput || kind == moderation.KindContentFilter {
			count, err := q.CountAttemptsByClass(ctx, moderationdb.CountAttemptsByClassParams{
				JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration, ErrorClass: &errorClass,
			})
			if err != nil {
				return err
			}
			classCount = int(count)
		}

		s.rngMu.Lock()
		decision := s.retry.decide(kind, int(c.AttemptNumber), classCount+1, s.rng)
		s.rngMu.Unlock()
		wait := decision.wait
		if kind == moderation.KindDurable && retryAfter > 0 {
			wait = retryAfter
			if wait > s.retry.MaxDelay {
				wait = s.retry.MaxDelay
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
				// Roll back the attempt update if its lease expired during finalization.
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
			// Roll back: a stale outbox row would block the valid owner on the generation key.
			return ErrFenced
		}
		return q.InsertOutbox(ctx, moderationdb.InsertOutboxParams{
			EventID: eventID, JobID: c.Job.ID, RunGeneration: c.Job.RunGeneration,
			PayloadVersion: 1, Payload: payload, AvailableAt: finishedAt,
		})
	})
}

func (s *Service) GetJobByID(ctx context.Context, id uuid.UUID) (moderationdb.ModerationJob, error) {
	job, err := moderationdb.New(s.pool).GetJobByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return moderationdb.ModerationJob{}, ErrJobNotFound
	}
	return job, err
}

func (s *Service) GetJobsForReviewer(
	ctx context.Context,
	reviewerID uuid.UUID,
	requestIDs []uuid.UUID,
) ([]moderationdb.ModerationJob, error) {
	return moderationdb.New(s.pool).GetJobsForReviewer(ctx, moderationdb.GetJobsForReviewerParams{
		ReviewerID: reviewerID, ModerationRequestIds: requestIDs,
	})
}

func (s *Service) GetAttempts(
	ctx context.Context,
	jobID uuid.UUID,
	runGeneration int32,
) ([]moderationdb.ModerationAttempt, error) {
	return moderationdb.New(s.pool).GetAttempts(ctx, moderationdb.GetAttemptsParams{
		JobID: jobID, RunGeneration: runGeneration,
	})
}

// validateAssessment enforces generic outcome and reason-code shape. The
// evaluator validates codes against its policy vocabulary.
func validateAssessment(a moderation.ReviewAssessment) error {
	switch a.Outcome {
	case moderation.OutcomeApproved:
		if len(a.ReasonCodes) > 0 {
			return moderation.WrapInvalidOutputError(errors.New("approved assessment must carry no reason codes"))
		}
		if a.PolicyVersion == "" {
			return moderation.WrapInvalidOutputError(errors.New("assessment missing policy version"))
		}
	case moderation.OutcomeRejected:
		if len(a.ReasonCodes) == 0 {
			return moderation.WrapInvalidOutputError(
				errors.New("rejected assessment must carry at least one reason code"),
			)
		}
		for _, code := range a.ReasonCodes {
			if strings.TrimSpace(code) == "" {
				return moderation.WrapInvalidOutputError(
					errors.New("rejected assessment contains an empty reason code"),
				)
			}
		}
		if a.PolicyVersion == "" {
			return moderation.WrapInvalidOutputError(errors.New("assessment missing policy version"))
		}
	default:
		return moderation.WrapInvalidOutputError(errors.New("assessment outcome is not approved or rejected"))
	}
	return nil
}

func exhaustJob(ctx context.Context, q *moderationdb.Queries, job moderationdb.ModerationJob) error {
	rows, err := q.ExhaustJob(ctx, moderationdb.ExhaustJobParams{ID: job.ID, RunGeneration: job.RunGeneration})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrFenced
	}
	eventID := uuid.New()
	now := time.Now()
	payload, err := buildOutboxPayload(eventID, jobSnapshot(job), moderationoutcomeTypeError(), nil, now)
	if err != nil {
		return err
	}
	return q.InsertOutbox(ctx, moderationdb.InsertOutboxParams{
		EventID:        eventID,
		JobID:          job.ID,
		RunGeneration:  job.RunGeneration,
		PayloadVersion: 1,
		Payload:        payload,
		AvailableAt:    now,
	})
}
