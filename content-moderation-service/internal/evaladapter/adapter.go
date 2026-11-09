// Package evaladapter is the single boundary outside llmpolicy that knows how
// the review-text policy maps its technical failures into the durable engine's
// retry metadata. The worker pool (jobs) classifies errors against
// jobs.ClassifiedError, but llmpolicy owns its own error types; this package
// wraps llmpolicy's Evaluator and translates its errors so the engine applies
// the correct retry kind, budget and recorded class without either policy or
// engine importing the other.
package evaladapter

import (
	"context"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
)

// Wrap wraps the llmpolicy evaluator as a jobs.Evaluator, translating every
// technical error it returns into a jobs.ClassifiedError. A nil error is passed
// through unchanged.
func Wrap(eval *llmpolicy.Evaluator) jobs.Evaluator {
	return wrapped{eval: eval}
}

type wrapped struct{ eval *llmpolicy.Evaluator }

func (f wrapped) Evaluate(ctx context.Context, input jobs.EvaluationInput) (jobs.Assessment, error) {
	assessment, err := f.eval.Evaluate(ctx, input)
	if err != nil {
		return jobs.Assessment{}, translate(err)
	}
	return assessment, nil
}

// Classify returns the stable, sanitized error class for a technical evaluation
// error, or "server_error" for anything unexpected. It is used by the
// configuration test so a draft test surfaces the same class the worker records.
func Classify(err error) string {
	if err == nil {
		return jobs.ErrorClassServerError
	}
	switch {
	case errors.Is(err, llmpolicy.ErrEmptyResponse):
		return jobs.ErrorClassInvalidOutput
	case errors.As(err, new(*llmpolicy.ErrInvalidOutput)):
		return jobs.ErrorClassInvalidOutput
	case errors.As(err, new(*llmpolicy.ErrRefusal)):
		return jobs.ErrorClassContentFilter
	case errors.As(err, new(*llmpolicy.ErrRateLimited)):
		return jobs.ErrorClassRateLimited
	case errors.As(err, new(*llmpolicy.ErrTimeout)):
		return jobs.ErrorClassTimeout
	case errors.As(err, new(*llmpolicy.ErrConfiguration)):
		return jobs.ErrorClassConfiguration
	case errors.As(err, new(*llmpolicy.ErrConnection)):
		return jobs.ErrorClassConnection
	case errors.As(err, new(*llmpolicy.ErrContext)):
		return jobs.ErrorClassServerError
	default:
		return jobs.ErrorClassServerError
	}
}

// translate maps one llmpolicy technical error into the jobs.ClassifiedError the
// engine classifies against. It never leaks provider detail beyond the bounded
// strings the policy already redacted.
func translate(err error) error {
	switch {
	case errors.Is(err, llmpolicy.ErrEmptyResponse):
		return jobs.WrapInvalidOutputError(err)
	case errors.As(err, new(*llmpolicy.ErrInvalidOutput)):
		return jobs.WrapInvalidOutputError(err)
	case errors.As(err, new(*llmpolicy.ErrRefusal)):
		return jobs.WrapContentFilterError(err)
	case errors.As(err, new(*llmpolicy.ErrRateLimited)):
		var rl *llmpolicy.ErrRateLimited
		_ = errors.As(err, &rl)
		return jobs.WrapRateLimitError(rl.RetryAfter, err)
	case errors.As(err, new(*llmpolicy.ErrTimeout)):
		return jobs.WrapTimeoutError(err)
	case errors.As(err, new(*llmpolicy.ErrConfiguration)):
		return jobs.WrapConfigurationError(err)
	case errors.As(err, new(*llmpolicy.ErrConnection)):
		return jobs.WrapConnectionError(err)
	case errors.As(err, new(*llmpolicy.ErrContext)):
		return jobs.WrapConnectionError(err)
	default:
		return jobs.WrapServerError(err)
	}
}
