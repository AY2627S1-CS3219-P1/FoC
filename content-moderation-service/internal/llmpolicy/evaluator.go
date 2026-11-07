// Package llmpolicy implements the review-text moderation policy: it owns the
// versioned rubric, prompt construction, output validation and the LLM
// evaluator that satisfies the job engine's Evaluator port.
package llmpolicy

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
)

// Evaluator implements jobs.Evaluator using an OpenAI-compatible chat
// completion backend. The composition root injects the completion client.
type Evaluator struct {
	client CompletionClient
}

// NewEvaluator constructs the LLM-backed evaluator.
func NewEvaluator(client CompletionClient) *Evaluator {
	return &Evaluator{client: client}
}

// Evaluate assesses one review comment against the versioned policy.
func (e *Evaluator) Evaluate(ctx context.Context, input jobs.EvaluationInput) (jobs.Assessment, error) {
	return jobs.Assessment{}, nil
}
