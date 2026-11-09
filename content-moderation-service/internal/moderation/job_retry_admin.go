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
) (JobView, error) {
	var updated JobView
	if err := database.Within(ctx, s.JobService.pool, func(q *moderationdb.Queries) error {
		var err error
		_, err = NewTransitions(q).
			Retry(ctx, jobID, expectedRunGeneration, useCurrentConfiguration, createdBy)
		if err != nil {
			return err
		}
		row, err := q.GetJobViewByID(ctx, jobID)
		updated = JobView{ModerationJob: row.ModerationJob, ConfigurationPaused: row.ConfigurationPaused}
		return err
	}); err != nil {
		return JobView{}, err
	}
	return updated, nil
}
