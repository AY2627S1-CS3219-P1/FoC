package job

import (
	"context"
	"errors"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrJobNotFound  = errors.New("moderation job not found")
	ErrNotRetriable = errors.New("job is not errored at the expected generation")
)

// RetryErroredJob fences the expected generation and records the retry with its audit atomically.
func (s *Service) RetryErroredJob(
	ctx context.Context,
	jobID uuid.UUID,
	expectedRunGeneration int32,
	createdBy string,
) (moderationdb.ModerationJob, error) {
	var updated moderationdb.ModerationJob
	err := database.Within(ctx, s.pool, func(q *moderationdb.Queries) error {
		job, err := q.LockJob(ctx, jobID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrJobNotFound
		}
		if err != nil {
			return err
		}
		if expectedRunGeneration < 1 || job.RunGeneration != expectedRunGeneration || job.Status != "errored" {
			return ErrNotRetriable
		}
		updated, err = q.RetryErroredJob(
			ctx,
			moderationdb.RetryErroredJobParams{ID: jobID, RunGeneration: expectedRunGeneration},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotRetriable
		}
		if err != nil {
			return err
		}
		return q.InsertAdminAudit(ctx, moderationdb.InsertAdminAuditParams{
			Action: "RETRY_JOB", JobID: &jobID, Actor: createdBy, Detail: []byte(`{"reason":"manual retry"}`),
		})
	})
	return updated, err
}
