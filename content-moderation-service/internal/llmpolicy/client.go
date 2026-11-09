package llmpolicy

import (
	"context"
	"time"
)

// OutputMode mirrors the configured structured-output selection.
type OutputMode = string

// OutputMode selection constants name the two structured-output modes. They
// mirror jobs.OutputModeJSONSchema / jobs.OutputModeJSONObject so policy tests
// and the adapter can reference them unqualified.
const (
	OutputModeJSONSchema OutputMode = "json_schema"
	OutputModeJSONObject OutputMode = "json_object"
)

// CompletionRequest is the service-neutral chat completion input. Ports use
// service-owned types, never generated SDK types. The evaluator builds the
// prompt; the adapter translates the transport.
type CompletionRequest struct {
	// Model is the configured chat-capable model identifier.
	Model string
	// System is the trusted instruction message: moderation role, versioned
	// rubric, data-not-instructions framing and exact output requirements.
	System string
	// User is the data message carrying the complete review comment, clearly
	// delimited. Never truncated or transformed.
	User string
	// OutputMode selects strict json_schema or json_object.
	OutputMode string
	// SchemaJSON is the code-owned JSON Schema for the assessment object.
	SchemaJSON string
	// Parameters carries the optional supported inference parameters.
	Parameters CompletionParameters
	// Timeout bounds the request; adapters enforce it on response reads too.
	Timeout time.Duration
}

// CompletionParameters carries optional inference parameters. Zero-valued
// pointer fields are omitted from the provider request, never sent as zero
// values.
type CompletionParameters struct {
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
	Advanced            map[string]string
}

// CompletionResponse is the service-neutral completion output.
type CompletionResponse struct {
	// Content is the first choice message content.
	Content string
	// FinishReason is the provider finish reason, e.g. "stop", "length".
	FinishReason string
	// Refusal is the provider refusal message, when present.
	Refusal string
	// Model echoes the model that served the request, when present.
	Model string
}

// CompletionClient is the transport port the evaluator consumes. The
// OpenAI-compatible adapter implements it. Implementations must not retry
// automatically; the persistent runner owns retry scheduling.
type CompletionClient interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}
