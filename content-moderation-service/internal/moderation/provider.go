package moderation

import (
	"context"
	"time"
)

// SDK types stay in the adapter; moderation owns this completion contract.
type CompletionRequest struct {
	Model string
	// Trusted instructions; never include review content.
	System string
	// Complete review data, delimited and never truncated.
	User       string
	OutputMode string
	SchemaJSON string
	Parameters CompletionParameters
	// Timeout bounds the request; adapters enforce it on response reads too.
	Timeout time.Duration
}

// Nil fields are omitted; non-nil pointers preserve explicit zero values.
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

type CompletionResponse struct {
	Content      string
	FinishReason string
	Refusal      string
	Model        string
}

// Implementations must not retry; durable job execution owns retry scheduling.
type CompletionClient interface {
	Complete(ctx context.Context, req *CompletionRequest) (*CompletionResponse, error)
}
