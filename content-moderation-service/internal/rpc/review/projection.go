package review

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	moderationjob "github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/job"
	reviewv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// statusFor maps a stored job status to the owner-visible enum. Unknown statuses
// map to the unspecified state rather than erroring.
func statusFor(status string) reviewv1.ModerationJobStatus {
	switch status {
	case "queued":
		return reviewv1.ModerationJobStatus_MODERATION_JOB_STATUS_QUEUED
	case "processing":
		return reviewv1.ModerationJobStatus_MODERATION_JOB_STATUS_PROCESSING
	case "retry_wait":
		return reviewv1.ModerationJobStatus_MODERATION_JOB_STATUS_RETRY_WAIT
	case "completed":
		return reviewv1.ModerationJobStatus_MODERATION_JOB_STATUS_COMPLETED
	case "errored":
		return reviewv1.ModerationJobStatus_MODERATION_JOB_STATUS_ERRORED
	default:
		return reviewv1.ModerationJobStatus_MODERATION_JOB_STATUS_UNSPECIFIED
	}
}

// outcomeFor maps a stored verdict to the owner-visible outcome. The outcome is
// only set once the job is terminal; a completed job carries APPROVED or
// REJECTED, an errored job carries ERROR, everything else is unspecified.
func outcomeFor(job moderationdb.ModerationJob) reviewv1.ModerationOutcomeType {
	switch job.Status {
	case "completed":
		if job.Outcome == nil {
			return reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_UNSPECIFIED
		}
		switch *job.Outcome {
		case "approved":
			return reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED
		case "rejected":
			return reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED
		default:
			return reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_UNSPECIFIED
		}
	case "errored":
		return reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
	default:
		return reviewv1.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_UNSPECIFIED
	}
}

// summarize renders the owner-visible summary of one job. Reason codes appear
// only for a rejected verdict, never for approval or error.
func summarize(view moderationjob.JobView) *reviewv1.ReviewModerationJobSummary {
	job := view.ModerationJob
	summary := &reviewv1.ReviewModerationJobSummary{
		JobId:               job.ID.String(),
		ModerationRequestId: job.ModerationRequestID.String(),
		ReviewId:            job.ReviewID.String(),
		Status:              statusFor(job.Status),
		AttemptCount:        job.AttemptCount,
		RunGeneration:       job.RunGeneration,
		PolicyVersion:       derefOr(job.PolicyVersion, ""),
		CreatedAt:           timestamppb.New(job.CreatedAt),
	}
	if job.NextAttemptAt != nil {
		summary.NextRetryAt = timestamppb.New(*job.NextAttemptAt)
	}
	if job.FinishedAt != nil {
		summary.FinishedAt = timestamppb.New(*job.FinishedAt)
	}
	summary.Outcome = outcomeFor(job)
	if job.Status == "completed" && job.Outcome != nil && *job.Outcome == "rejected" {
		summary.ReasonCodes = job.ReasonCodes
	}
	return summary
}

// attempt renders one recorded attempt. Only classified, bounded fields are
// exposed; the raw diagnostic (Detail) is deliberately dropped.
func attemptOf(m moderationdb.ModerationAttempt) *reviewv1.ModerationAttempt {
	a := &reviewv1.ModerationAttempt{
		RunGeneration: m.RunGeneration,
		AttemptNumber: m.AttemptNumber,
		ModelId:       derefOr(m.ModelID, ""),
		Outcome:       m.Outcome,
		ErrorClass:    derefOr(m.ErrorClass, ""),
		StartedAt:     timestamppb.New(m.StartedAt),
	}
	if m.FinishedAt != nil {
		a.FinishedAt = timestamppb.New(*m.FinishedAt)
	}
	return a
}

func derefOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}
