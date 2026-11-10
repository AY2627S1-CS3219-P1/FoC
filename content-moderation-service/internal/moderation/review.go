package moderation

type ModerationOutcome string

const (
	OutcomeApproved ModerationOutcome = "approved"
	OutcomeRejected ModerationOutcome = "rejected"
	// Technical failure to assess; must carry no content reasons.
	OutcomeError ModerationOutcome = "error"
)

type ReviewInput struct {
	ModerationRequestID string
	ReviewID            string
	ReviewerID          string
	ContentRevision     int32
	// Comment is the complete immutable review snapshot; never truncated.
	Comment string
}

type OutputMode string

const (
	OutputModeJSONSchema = "json_schema"
	OutputModeJSONObject = "json_object"
)

// Valid only when AssessReview returns no error.
type ReviewAssessment struct {
	Outcome ModerationOutcome
	// ReasonCodes are policy-owned identifiers; the job engine stores them opaquely.
	ReasonCodes []string
	// Historical rubric/schema version; does not select runtime settings.
	PolicyVersion string
}
