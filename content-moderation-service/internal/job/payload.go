package job

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"time"

	moderationevents "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Rejections must retain their outcome: User drops ERROR events carrying content reasons.
func outcomeTypeFor(outcome moderation.ModerationOutcome) moderationevents.ModerationOutcomeType {
	switch outcome {
	case moderation.OutcomeApproved:
		return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED
	case moderation.OutcomeRejected:
		return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED
	default:
		return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
	}
}

func moderationoutcomeTypeError() moderationevents.ModerationOutcomeType {
	return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
}

// Build from saved state so outbox retries preserve event identity and content.
func buildOutboxPayload(
	eventID uuid.UUID,
	job JobSnapshot,
	outcome moderationevents.ModerationOutcomeType,
	reasonCodes []string,
	finishedAt time.Time,
) ([]byte, error) {
	policyVersion := ""
	if job.PolicyVersion != nil {
		policyVersion = *job.PolicyVersion
	}
	finished := &moderationevents.ReviewModerationFinished{
		EventId:             eventID.String(),
		JobId:               job.ID.String(),
		ModerationRequestId: job.ModerationRequestID.String(),
		SourceEventId:       job.SourceEventID.String(),
		ReviewId:            job.ReviewID.String(),
		ReviewerId:          job.ReviewerID.String(),
		ContentRevision:     job.ContentRevision,
		RunGeneration:       job.RunGeneration,
		PolicyVersion:       policyVersion,
		Outcome:             outcome,
		ReasonCodes:         reasonCodes,
		FinishedAt:          timestamppb.New(finishedAt),
	}

	return protojson.Marshal(finished)
}

func reasonCodesFor(outcome moderation.ModerationOutcome, reasonCodes []string) []string {
	if outcome == moderation.OutcomeRejected {
		return reasonCodes
	}
	return nil
}
