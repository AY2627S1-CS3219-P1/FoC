package job

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrJobNotFound  = errors.New("moderation job not found")
	ErrNotRetriable = errors.New("job is not errored at the expected generation")
)

// Transitions applies administrative retries inside the caller transaction.
type Transitions struct{ queries *moderationdb.Queries }

func NewTransitions(queries *moderationdb.Queries) *Transitions {
	return &Transitions{queries: queries}
}

func (s *Transitions) Retry(
	ctx context.Context,
	id uuid.UUID,
	generation int32,
	actor string,
) (moderationdb.ModerationJob, error) {
	q := s.queries
	job, err := q.LockJob(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return moderationdb.ModerationJob{}, ErrJobNotFound
	}
	if err != nil {
		return moderationdb.ModerationJob{}, err
	}
	if generation < 1 || job.RunGeneration != generation || job.Status != "errored" {
		return moderationdb.ModerationJob{}, ErrNotRetriable
	}
	rows, err := q.RetryErroredJob(
		ctx,
		moderationdb.RetryErroredJobParams{
			ID:            id,
			RunGeneration: generation,
		},
	)
	if err != nil {
		return moderationdb.ModerationJob{}, err
	}
	if rows == 0 {
		return moderationdb.ModerationJob{}, ErrNotRetriable
	}
	detail, _ := json.Marshal(map[string]string{"reason": "manual retry"})
	if err := q.InsertAdminAudit(
		ctx,
		moderationdb.InsertAdminAuditParams{Action: "RETRY_JOB", JobID: &id, Actor: actor, Detail: detail},
	); err != nil {
		return moderationdb.ModerationJob{}, err
	}
	return q.GetJobByID(ctx, id)
}
