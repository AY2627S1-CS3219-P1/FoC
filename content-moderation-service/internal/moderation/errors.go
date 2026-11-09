// Package moderation owns review moderation jobs, LLM assessment, and admin configuration.
package moderation

import (
	"errors"
	"time"
)

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
// Internal operations use this when attaching an explicit failure class.
// Provider and response-validation errors are classified directly by Classify.
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

// Classify is the single mapping from concrete failures to stored classes and retry handling.
func Classify(err error) (string, errorKind, time.Duration) {
	var ce *ClassifiedError
	if errors.As(err, &ce) {
		return ce.ErrorClass, ce.Kind, ce.RetryAfter
	}
	switch {
	case errors.Is(err, ErrEmptyResponse), errors.As(err, new(*ErrInvalidOutput)):
		return ErrorClassInvalidOutput, kindInvalidOutput, 0
	case errors.As(err, new(*ErrRefusal)):
		return ErrorClassContentFilter, kindContentFilter, 0
	case errors.As(err, new(*ErrRateLimited)):
		var rate *ErrRateLimited
		errors.As(err, &rate)
		return ErrorClassRateLimited, kindDurable, rate.RetryAfter
	case errors.As(err, new(*ErrTimeout)):
		return ErrorClassTimeout, kindDurable, 0
	case errors.As(err, new(*ErrConfiguration)):
		return ErrorClassConfiguration, kindConfiguration, 0
	case errors.As(err, new(*ErrConnection)), errors.As(err, new(*ErrContext)):
		return ErrorClassConnection, kindDurable, 0
	}
	return ErrorClassServerError, kindDurable, 0
}
