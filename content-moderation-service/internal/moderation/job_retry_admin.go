package moderation

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
)

// RetryErroredJob fences the expected generation and records the retry with its audit atomically.
func (s *Service) RetryErroredJob(
	ctx context.Context,
	jobID uuid.UUID,
	expectedRunGeneration int32,
	useCurrentConfiguration bool,
	createdBy string,
) (moderationdb.ModerationJob, error) {
	var updated moderationdb.ModerationJob
	if err := database.Within(ctx, s.JobService.pool, func(q *moderationdb.Queries) error {
		var err error
		updated, err = NewTransitions(q).
			Retry(ctx, jobID, expectedRunGeneration, useCurrentConfiguration, createdBy)
		return err
	}); err != nil {
		return moderationdb.ModerationJob{}, err
	}
	return updated, nil
}
