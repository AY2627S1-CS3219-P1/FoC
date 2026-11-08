package jobs

import (
	"time"

	moderationevents "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// outcomeTypeFor maps a terminal assessment outcome to the published enum.
// Rejections must publish REJECTED (with their reason codes): user-service
// drops an ERROR event that carries content reasons, so mapping a rejection to
// ERROR would silently discard every rejection.
func outcomeTypeFor(outcome ModerationOutcome) moderationevents.ModerationOutcomeType {
	switch outcome {
	case OutcomeApproved:
		return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_APPROVED
	case OutcomeRejected:
		return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_REJECTED
	default:
		return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
	}
}

// moderationoutcomeTypeError is the published enum for a terminal error.
func moderationoutcomeTypeError() moderationevents.ModerationOutcomeType {
	return moderationevents.ModerationOutcomeType_MODERATION_OUTCOME_TYPE_ERROR
}

// buildOutboxPayload renders the terminal event from the persisted job. The
// payload is built from saved state, never from evaluator output, so a
// redelivery after failure re-emits byte-identical content with the same
// event_id.
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
	configVersion := ""
	if job.ConfigVersionID != nil {
		configVersion = *job.ConfigVersionID
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
		ConfigVersionId:     configVersion,
		Outcome:             outcome,
		ReasonCodes:         reasonCodes,
		FinishedAt:          timestamppb.New(finishedAt),
	}

	return protojson.Marshal(finished)
}

// reasonCodesOrDefault returns reason codes for a rejected assessment, empty
// otherwise, so approved and error events never carry content reasons.
func reasonCodesFor(outcome ModerationOutcome, reasonCodes []string) []string {
	if outcome == OutcomeRejected {
		return reasonCodes
	}
	return nil
}
