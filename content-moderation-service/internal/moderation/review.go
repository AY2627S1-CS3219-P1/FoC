package moderation

import "context"

// ModerationOutcome is the terminal business result of an assessment.
type ModerationOutcome string

const (
	// OutcomeApproved means the content passed the policy. Requires no reason
	// codes.
	OutcomeApproved ModerationOutcome = "approved"
	// OutcomeRejected means the content violated the policy. Requires at
	// least one reason code.
	OutcomeRejected ModerationOutcome = "rejected"
	// OutcomeError means no valid content verdict was obtained; a technical
	// outcome, never a content judgement. Must not carry reason codes.
	OutcomeError ModerationOutcome = "error"
)

// ReviewInput is the immutable job snapshot passed to Service.AssessReview.
type ReviewInput struct {
	// ModerationRequestID identifies the assessment request.
	ModerationRequestID string
	ReviewID            string
	ReviewerID          string
	ContentRevision     int32
	// Comment is the complete immutable review snapshot; never truncated.
	Comment string
	// Config is the effective immutable configuration pinned for this run.
	Config ReviewConfig
}

// OutputMode selects how the service requests structured LLM output.
type OutputMode string

const (
	// OutputModeJSONSchema requests strict response_format json_schema.
	OutputModeJSONSchema = "json_schema"
	// OutputModeJSONObject requests response_format json_object; local schema
	// validation still applies.
	OutputModeJSONObject = "json_object"
)

// ReviewConfig is the pinned immutable evaluation configuration.
type ReviewConfig struct {
	SystemMessage string
	VersionID     string
	ModelID       string
	OutputMode    OutputMode

	Temperature      *float64
	TopP             *float64
	PresencePenalty  *float64
	FrequencyPenalty *float64
	// MaxCompletionTokens or MaxTokens is set, never both.
	MaxCompletionTokens *int64
	MaxTokens           *int64
	Stop                []string
	Seed                *int64
	ReasoningEffort     string
	// Advanced holds validated additional provider parameters from the
	// service-owned allowlist.
	Advanced map[string]string
}

// ReviewAssessment is a validated review result. An error return means the
// evaluation failed technically; ReviewAssessment is then unused.
type ReviewAssessment struct {
	Outcome ModerationOutcome
	// ReasonCodes are policy-owned identifiers; the job engine stores them opaquely.
	ReasonCodes []string
	// PolicyVersion is the code-owned rubric/schema version that judged the
	// content, e.g. "review-text-v1".
	PolicyVersion string
}

// reviewAssessmentService is the worker's view of Service.AssessReview.
type reviewAssessmentService interface {
	AssessReview(ctx context.Context, input ReviewInput) (ReviewAssessment, error)
}
