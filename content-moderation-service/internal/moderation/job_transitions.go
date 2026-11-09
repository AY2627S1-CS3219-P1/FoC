package moderation

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrConfigurationConflict = errors.New("active configuration changed")
	ErrJobNotFound           = errors.New("moderation job not found")
	ErrNotRetriable          = errors.New("job is not errored at the expected generation")
)

// Transitions centralizes administrative job writes inside the caller's local
// transaction. Config activation and recovery therefore commit or roll back together.
// This is a PostgreSQL adapter; worker application code only consumes jobOperations.
type Transitions struct{ queries *moderationdb.Queries }

func NewTransitions(queries *moderationdb.Queries) *Transitions {
	return &Transitions{queries: queries}
}

func (s *Transitions) Activate(
	ctx context.Context,
	versionID uuid.UUID,
	actor string,
	expected *uuid.UUID,
	resume bool,
) error {
	q := s.queries
	rows, err := q.ActivateConfigVersion(ctx, moderationdb.ActivateConfigVersionParams{
		ActiveVersionID: &versionID, ActivatedBy: &actor, ExpectedVersionID: expected,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrConfigurationConflict
	}
	if resume {
		version, err := q.GetConfigVersionById(ctx, versionID)
		if err != nil {
			return err
		}
		id := versionID.String()
		ids, err := q.ResumeConfigurationPausedJobs(
			ctx,
			moderationdb.ResumeConfigurationPausedJobsParams{
				ConfigVersionID: &id,
				PolicyVersion:   &version.PolicyVersion,
			},
		)
		if err != nil {
			return err
		}
		for _, jobID := range ids {
			if err := q.FinishResumedAttempts(ctx, jobID); err != nil {
				return err
			}
			if err := q.InsertAdminAudit(ctx, moderationdb.InsertAdminAuditParams{
				Action:          "RESUME_CONFIGURATION_PAUSED_JOB",
				JobID:           &jobID,
				ConfigVersionID: &versionID,
				Actor:           actor,
				Detail:          []byte(`{}`),
			}); err != nil {
				return err
			}
		}
	}
	if err := q.UnpauseConfiguration(ctx, versionID); err != nil {
		return err
	}
	detail, err := json.Marshal(map[string]any{"expected_active_version_id": expected})
	if err != nil {
		return err
	}
	return q.InsertAdminAudit(
		ctx,
		moderationdb.InsertAdminAuditParams{
			Action:          "ACTIVATE_CONFIG",
			ConfigVersionID: &versionID,
			Actor:           actor,
			Detail:          detail,
		},
	)
}

func (s *Transitions) Retry(
	ctx context.Context,
	id uuid.UUID,
	generation int32,
	useCurrent bool,
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
	configID, policy := job.ConfigVersionID, job.PolicyVersion
	if useCurrent {
		active, err := q.GetActiveConfigVersionFull(ctx)
		if err != nil {
			return moderationdb.ModerationJob{}, err
		}
		id := active.VersionID.String()
		configID = &id
		policy = &active.PolicyVersion
	}
	rows, err := q.RetryErroredJob(
		ctx,
		moderationdb.RetryErroredJobParams{
			ID:              id,
			RunGeneration:   generation,
			ConfigVersionID: configID,
			PolicyVersion:   policy,
		},
	)
	if err != nil {
		return moderationdb.ModerationJob{}, err
	}
	if rows == 0 {
		return moderationdb.ModerationJob{}, ErrNotRetriable
	}
	detail, _ := json.Marshal(map[string]bool{"use_current_configuration": useCurrent})
	if err := q.InsertAdminAudit(
		ctx,
		moderationdb.InsertAdminAuditParams{Action: "RETRY_JOB", JobID: &id, Actor: actor, Detail: detail},
	); err != nil {
		return moderationdb.ModerationJob{}, err
	}
	return q.GetJobByID(ctx, id)
}
