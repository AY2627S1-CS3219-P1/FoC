// Package jobs implements the durable review-moderation engine: ingestion
// deduplication, a bounded per-process worker pool, replica-safe claims with
// leases and fencing, persistent retries, attempt history and the terminal
// outbox transition. The worker engine depends only on the Evaluator port
// defined here, so the LLM adapter is an implementation detail.
package jobs

import (
	"errors"
	"time"
)

// ErrSerializationFailure signals a Postgres serialization/deadlock victim. The
// caller may retry the transaction without losing committed work.
var ErrSerializationFailure = errors.New("serialization failure")

// ErrNoActiveConfiguration is returned by the claim step when no moderation
// configuration has been activated yet. It is not a failure: the job simply
// stays queued until a configuration is activated.
var ErrNoActiveConfiguration = errors.New("no active moderation configuration")

// ErrClaimLost is set when a worker's heartbeat finds its claim gone: another
// worker reclaimed the job after a lease expiry, or the claim was fenced away.
// The in-flight assessment is discarded and the job is left for the winner.
var ErrClaimLost = errors.New("moderation job claim was lost")

// ErrorClass is the stable sanitized label recorded on a job or attempt. It
// never contains credentials, provider bodies or the prompt.
const (
	ErrorClassConnection    = "connection"
	ErrorClassTimeout       = "timeout"
	ErrorClassServerError   = "server_error"
	ErrorClassRateLimited   = "rate_limited"
	ErrorClassInvalidOutput = "invalid_output"
	ErrorClassContentFilter = "content_filter"
	ErrorClassConfiguration = "configuration"
)

// errorKind drives the retry policy. It is distinct from the surfaced
// ErrorClass: for example both "timeout" and "server_error" use a durable
// retry, while "invalid_output" uses a small fixed budget.
type errorKind string

const (
	kindDurable       errorKind = "durable"
	kindInvalidOutput errorKind = "invalid_output"
	kindContentFilter errorKind = "content_filter"
	kindConfiguration errorKind = "configuration"
)

// ClassifiedError wraps an underlying error with the moderation retry metadata.
// The LLM evaluator returns these so the worker can schedule retries without
// the engine knowing the provider. Unknown (unclassified) errors are treated as
// durable and counted, so they still reach a terminal error within budget.
type ClassifiedError struct {
	Err        error
	ErrorClass string
	Kind       errorKind
	// RetryAfter is the provider-suggested delay, honoured for rate limits.
	RetryAfter time.Duration
}

func (e *ClassifiedError) Error() string { return e.Err.Error() }
func (e *ClassifiedError) Unwrap() error { return e.Err }

func wrap(kind errorKind, errorClass string, retryAfter time.Duration, err error) *ClassifiedError {
	return &ClassifiedError{Err: err, ErrorClass: errorClass, Kind: kind, RetryAfter: retryAfter}
}

// WrapConnectionError marks a transport/connection failure as a durable retry.
func WrapConnectionError(err error) *ClassifiedError {
	return wrap(kindDurable, ErrorClassConnection, 0, err)
}

// WrapTimeoutError marks a request timeout as a durable retry.
func WrapTimeoutError(err error) *ClassifiedError {
	return wrap(kindDurable, ErrorClassTimeout, 0, err)
}

// WrapServerError marks a 5xx provider error as a durable retry.
func WrapServerError(err error) *ClassifiedError {
	return wrap(kindDurable, ErrorClassServerError, 0, err)
}

// WrapRateLimitError marks a rate limit as a durable retry that honours the
// provider's retry-after hint.
func WrapRateLimitError(retryAfter time.Duration, err error) *ClassifiedError {
	return wrap(kindDurable, ErrorClassRateLimited, retryAfter, err)
}

// WrapInvalidOutputError marks malformed, truncated or otherwise invalid
// structured output. It counts against a small budget, then errors.
func WrapInvalidOutputError(err error) *ClassifiedError {
	return wrap(kindInvalidOutput, ErrorClassInvalidOutput, 0, err)
}

// WrapContentFilterError marks a provider refusal or content filter. No verdict
// is inferred; bounded recovery then errors.
func WrapContentFilterError(err error) *ClassifiedError {
	return wrap(kindContentFilter, ErrorClassContentFilter, 0, err)
}

// WrapConfigurationError marks bad credentials, an unsupported model or
// parameter. It pauses the job without exhausting the retry budget, so fixing
// the configuration (or an explicit admin retry) resumes processing.
func WrapConfigurationError(err error) *ClassifiedError {
	return wrap(kindConfiguration, ErrorClassConfiguration, 0, err)
}

// classify resolves an evaluator error into its classification. Non-classified
// errors default to a durable, counted retry.
// Classify extracts the error classification from a wrapped error.
func Classify(err error) (string, errorKind, time.Duration) {
	var ce *ClassifiedError
	if errors.As(err, &ce) {
		return ce.ErrorClass, ce.Kind, ce.RetryAfter
	}
	return ErrorClassServerError, kindDurable, 0
}
