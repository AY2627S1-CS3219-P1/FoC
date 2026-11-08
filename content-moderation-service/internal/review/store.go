package review

import (
	"context"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound reports the requested job has no moderation_job row. A missing job
// is valid from the caller's point of view (not yet ingested), so it maps to
// a caller-facing not_found rather than a generic error.
var ErrNotFound = errors.New("review: job not found")

// ErrNotRetriable reports the retry was refused: the job is not a terminal errored
// job at the expected run_generation, so no row changed. It maps to
// failed_precondition.
var ErrNotRetriable = errors.New("review: job is not a terminal errored job at the expected generation")

// Store owns the read model and administrative transitions for moderation jobs.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore binds the job store to one connection pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// GetJobByID returns the owner-visible summary of one job by primary key, or
// ErrNotFound. A missing job is valid from the caller's point of view (not yet
// ingested), so it maps to a caller-facing sentinel rather than a generic error.
func (s *Store) GetJobByID(ctx context.Context, id uuid.UUID) (*reviewv1.ReviewModerationJobSummary, error) {
	job, err := s.fetchJob(ctx, id)
	if err != nil {
		return nil, err
	}
	return Summarize(job), nil
}

// GetModerationJob returns the bounded admin view of one job: its summary plus
// the sanitized per-attempt history for the job's current run generation. A
// missing job returns ErrNotFound.
func (s *Store) GetModerationJob(
	ctx context.Context,
	jobID uuid.UUID,
) (*reviewv1.GetReviewModerationJobResponse, error) {
	job, err := s.fetchJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	attempts, err := s.GetAttempts(ctx, jobID, job.RunGeneration)
	if err != nil {
		return nil, err
	}
	return &reviewv1.GetReviewModerationJobResponse{
		Job:      Summarize(job),
		Attempts: attempts,
	}, nil
}

// fetchJob reads one job by primary key, translating a missing row into
// ErrNotFound.
func (s *Store) fetchJob(ctx context.Context, id uuid.UUID) (moderationdb.ModerationJob, error) {
	job, err := moderationdb.New(s.pool).GetJobByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return moderationdb.ModerationJob{}, ErrNotFound
	}
	return job, err
}

// GetJobsForReviewer returns the owner-visible summaries for the reviewer's
// requested moderation request IDs, oldest-first by creation.
func (s *Store) GetJobsForReviewer(
	ctx context.Context,
	reviewerID uuid.UUID,
	moderationRequestIDs []uuid.UUID,
) ([]*reviewv1.ReviewModerationJobSummary, error) {
	// Enforce the caller contract before any lookup: the request must not reach
	// the store with an unrelated reviewer_id.
	rows, err := moderationdb.New(s.pool).GetJobsForReviewer(ctx, moderationdb.GetJobsForReviewerParams{
		ReviewerID:           reviewerID,
		ModerationRequestIds: moderationRequestIDs,
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]*reviewv1.ReviewModerationJobSummary, 0, len(rows))
	for _, row := range rows {
		summaries = append(summaries, Summarize(row))
	}
	return summaries, nil
}

// GetAttempts returns the sanitized per-attempt history for a job's current
// generation, oldest first.
func (s *Store) GetAttempts(
	ctx context.Context,
	jobID uuid.UUID,
	runGeneration int32,
) ([]*reviewv1.ModerationAttempt, error) {
	rows, err := moderationdb.New(s.pool).GetAttempts(ctx, moderationdb.GetAttemptsParams{
		JobID:         jobID,
		RunGeneration: runGeneration,
	})
	if err != nil {
		return nil, err
	}
	attempts := make([]*reviewv1.ModerationAttempt, 0, len(rows))
	for _, row := range rows {
		attempts = append(attempts, attemptOf(row))
	}
	return attempts, nil
}

// RetryErroredJob requeues a terminal errored job exactly once for the expected
// generation, pinning either the active configuration or the one the job first
// ran under. It returns the updated job summary on success. A generation mismatch
// or a non-errored job returns ErrNotRetriable; a missing job returns
// ErrNotFound.
func (s *Store) RetryErroredJob(
	ctx context.Context,
	jobID uuid.UUID,
	expectedRunGeneration int32,
	useCurrentConfiguration bool,
	createdBy string,
) (*reviewv1.ReviewModerationJobSummary, error) {
	// Read the current state first so a missing job reports not_found rather
	// than silently failing the fenced update.
	job, err := s.fetchJob(ctx, jobID)
	if err != nil {
		return nil, err
	}

	configVersionID, policyVersion := s.retryConfig(ctx, job, useCurrentConfiguration)

	var updated moderationdb.ModerationJob
	if err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		rows, err := q.RetryErroredJob(ctx, moderationdb.RetryErroredJobParams{
			ConfigVersionID: configVersionID,
			PolicyVersion:   policyVersion,
			ID:              jobID,
			RunGeneration:   expectedRunGeneration,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrNotRetriable
		}
		if err := q.InsertAdminAudit(ctx, moderationdb.InsertAdminAuditParams{
			Action: "RETRY_JOB",
			JobID:  &jobID,
			Actor:  createdBy,
			Detail: jsonBool("use_current_configuration", useCurrentConfiguration),
		}); err != nil {
			return err
		}
		updated, err = q.GetJobByID(ctx, jobID)
		return err
	}); err != nil {
		return nil, err
	}
	return Summarize(updated), nil
}

// retryConfig resolves the configuration to persist on retry. Retaining uses the
// config and policy the job pinned at first run; pinning active reads the current
// active configuration version.
func (s *Store) retryConfig(
	ctx context.Context,
	job moderationdb.ModerationJob,
	useCurrent bool,
) (*string, *string) {
	if !useCurrent {
		return job.ConfigVersionID, job.PolicyVersion
	}
	active, err := moderationdb.New(s.pool).GetActiveConfigVersionFull(ctx)
	if err != nil {
		// No configuration activated yet means there is nothing to pin; the
		// fenced update then cannot succeed and the engine reports not retriable.
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, nil
	}
	versionID := active.VersionID.String()
	policy := active.PolicyVersion
	return &versionID, &policy
}

func jsonBool(key string, value bool) []byte {
	return []byte(`{"` + key + `": ` + boolString(value) + `}`)
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
