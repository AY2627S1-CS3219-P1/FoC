package moderation

import "context"

// SDK types stay in the adapter; moderation owns this completion contract.
type CompletionRequest struct {
	Model  string
	System string
	User   string
}

type CompletionResponse struct {
	Content      string
	FinishReason string
	Refusal      string
}

// Implementations must not retry; durable job execution owns retry scheduling.
type CompletionClient interface {
	Complete(context.Context, *CompletionRequest) (*CompletionResponse, error)
}
