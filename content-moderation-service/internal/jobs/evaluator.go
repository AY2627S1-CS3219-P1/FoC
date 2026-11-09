package jobs

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

// EvaluationInput is the immutable per-run input to the Evaluator port.
type EvaluationInput struct {
	// ModerationRequestID identifies the assessment request.
	ModerationRequestID string
	ReviewID            string
	ReviewerID          string
	ContentRevision     int32
	// Comment is the complete immutable review snapshot; never truncated.
	Comment string
	// Config is the effective immutable configuration pinned for this run.
	Config EffectiveConfig
}

// OutputMode selects how the evaluator requests structured output.
type OutputMode string

const (
	// OutputModeJSONSchema requests strict response_format json_schema.
	OutputModeJSONSchema OutputMode = "json_schema"
	// OutputModeJSONObject requests response_format json_object; local schema
	// validation still applies.
	OutputModeJSONObject OutputMode = "json_object"
)

// EffectiveConfig is the pinned immutable evaluation configuration.
type EffectiveConfig struct {
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

// Assessment is a validated evaluator result. An error return means the
// evaluation failed technically; Assessment is then unused.
type Assessment struct {
	Outcome ModerationOutcome
	// ReasonCodes are policy-owned identifiers; the job engine stores them opaquely.
	ReasonCodes []string
	// PolicyVersion is the code-owned rubric/schema version that judged the
	// content, e.g. "review-text-v1".
	PolicyVersion string
}

// Evaluator applies one assessment. Implementations must be safe for
// concurrent calls; the worker pool bounds concurrent executions per process.
type Evaluator interface {
	Evaluate(ctx context.Context, input EvaluationInput) (Assessment, error)
}
