package jobs

import (
	"context"
	"errors"
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
	pool                *pgxpool.Pool
	policy              Policy
	providerBaseURL     string
	providerFingerprint func(string, string) string
	// rngMu guards rng: one jobStore is shared by every pool worker, and
	// math/rand.Rand is not safe for concurrent use.
	rngMu sync.Mutex
	rng   *rand.Rand
}

type StoreOption func(*jobStore)

// WithProviderIdentity makes queued work wait for explicit revalidation after an endpoint change.
func WithProviderIdentity(baseURL string, fingerprint func(string, string) string) StoreOption {
	return func(store *jobStore) {
		store.providerBaseURL = baseURL
		store.providerFingerprint = fingerprint
	}
}

func NewJobStore(pool *pgxpool.Pool, policy Policy, rng *rand.Rand, options ...StoreOption) *jobStore {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	store := &jobStore{pool: pool, policy: policy, rng: rng}
	for _, option := range options {
		option(store)
	}
	return store
}

// Heartbeat extends the claim's lease while an assessment runs. The boolean is
// false when the claim vanished (lost lease), so the worker can cancel.
func (s *jobStore) Heartbeat(
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

// Release returns owned work on shutdown, requeueing it so a later cycle
// retries promptly. The boolean is false when the claim was already gone.
func (s *jobStore) Release(ctx context.Context, jobID uuid.UUID, claimToken uuid.UUID) (bool, error) {
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
		released = true
		return q.FinishAttempt(ctx, moderationdb.FinishAttemptParams{
			JobID: job.ID, RunGeneration: job.RunGeneration, AttemptNumber: job.AttemptCount,
			Outcome: "released",
		})
	})
	return released, err
}

// Claim performs the short claim transaction: lock the oldest eligible job, pin
// its active configuration before first inference, mark it processing, and
// record the started attempt. The claim lock is released by the transaction, so
// no lock is held during inference.
func (s *jobStore) Claim(ctx context.Context, workerID string, lease time.Duration) (ClaimOutcome, *ClaimedJob, error) {
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
		if int(job.AttemptCount) >= s.policy.MaxAttempts {
			if err := exhaustJob(ctx, q, job); err != nil {
				return err
			}
			outcome = ClaimExhausted
			return nil
		}

		var config moderationdb.ModerationConfigVersion
		if job.ConfigVersionID != nil {
			pinnedID, err := uuid.Parse(*job.ConfigVersionID)
			if err != nil {
				return err
			}
			config, err = q.GetConfigVersionById(ctx, pinnedID)
			if err != nil {
				return err
			}
		} else {
			config, err = q.GetActiveConfigVersionFull(ctx)
			if errors.Is(err, pgx.ErrNoRows) {
				outcome = ClaimNoConfiguration
				return nil
			}
			if err != nil {
				return err
			}
		}

		if s.providerBaseURL != "" && s.providerFingerprint != nil &&
			config.ProviderFingerprint != s.providerFingerprint(s.providerBaseURL, config.ModelID) {
			if err := q.PauseConfiguration(ctx, config.VersionID); err != nil {
				return err
			}
			outcome = ClaimNoConfiguration
			return nil
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
		attemptNumber := job.AttemptCount + 1
		modelID := config.ModelID

		if err := q.ClaimJob(ctx, moderationdb.ClaimJobParams{
			ClaimToken:   &claimToken,
			WorkerID:     &workerID,
			LeaseSeconds: lease.Seconds(),
			ID:           job.ID,
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
		claimed = &ClaimedJob{
			Job:           jobSnapshot(job),
			ClaimToken:    claimToken,
			AttemptNumber: attemptNumber,
			Config:        configurationSnapshot(config),
		}
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
	if err := validateAssessment(assessment); err != nil {
		return err
	}
	if assessment.PolicyVersion != c.Config.PolicyVersion || c.Job.PolicyVersion == nil ||
		assessment.PolicyVersion != *c.Job.PolicyVersion {
		return WrapConfigurationError(errors.New("assessment policy differs from pinned configuration"))
	}
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

// FinalizeFailed records a failed attempt and either schedules a bounded retry
// or errors the job terminally, enqueuing the outbox event only on terminal
// failure.
func (s *jobStore) FinalizeFailed(ctx context.Context, c ClaimedJob, evalErr error) error {
	errorClass, kind, retryAfter := Classify(evalErr)
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
		decision := s.policy.decide(kind, int(c.AttemptNumber), int(invalidCount)+1, int(filterCount)+1, s.rng)
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
				// Roll back the attempt update if its lease expired during finalization.
				return ErrFenced
			}
			if kind == kindConfiguration {
				return q.PauseConfiguration(ctx, c.Config.VersionID)
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
			PayloadVersion: 1, Payload: payload, AvailableAt: finishedAt,
		})
	})
}

func (s *jobStore) GetJobByID(ctx context.Context, id uuid.UUID) (moderationdb.ModerationJob, error) {
	return moderationdb.New(s.pool).GetJobByID(ctx, id)
}

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

func (s *jobStore) GetAttempts(
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
			if strings.TrimSpace(code) == "" {
				return WrapInvalidOutputError(errors.New("rejected assessment contains an empty reason code"))
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
