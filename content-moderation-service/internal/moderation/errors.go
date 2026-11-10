// Package moderation assesses review content using a fixed policy.
package moderation

import (
	"errors"
	"time"
)

// ErrorClass is the stable sanitized label recorded on a job or attempt. It
// never contains credentials, provider bodies or the prompt.
const (
	ErrorClassConnection    = "connection"
	ErrorClassTimeout       = "timeout"
	ErrorClassServerError   = "server_error"
	ErrorClassRateLimited   = "rate_limited"
	ErrorClassInvalidOutput = "invalid_output"
	// ErrorClassContentFilter records that the provider refused or filtered its
	// response. It is a technical failure to assess, never a review verdict.
	ErrorClassContentFilter = "content_filter"
	ErrorClassConfiguration = "configuration"
)

// ErrorKind drives the retry policy. It is distinct from the surfaced
// ErrorClass: for example both "timeout" and "server_error" use a durable
// retry, while "invalid_output" uses a small fixed budget.
type ErrorKind string

const (
	KindDurable       ErrorKind = "durable"
	KindInvalidOutput ErrorKind = "invalid_output"
	// Provider refusals and filtered responses share one bounded retry budget.
	KindContentFilter ErrorKind = "content_filter"
	KindConfiguration ErrorKind = "configuration"
)

// ClassifiedError wraps an underlying error with the moderation retry metadata.
// Internal operations use this when attaching an explicit failure class.
// Provider and response-validation errors are classified directly by Classify.
type ClassifiedError struct {
	Err        error
	ErrorClass string
	Kind       ErrorKind
	// RetryAfter is the provider-suggested delay, honoured for rate limits.
	RetryAfter time.Duration
}

func (e *ClassifiedError) Error() string { return e.Err.Error() }
func (e *ClassifiedError) Unwrap() error { return e.Err }

func wrap(kind ErrorKind, errorClass string, retryAfter time.Duration, err error) *ClassifiedError {
	return &ClassifiedError{Err: err, ErrorClass: errorClass, Kind: kind, RetryAfter: retryAfter}
}

// WrapConnectionError marks a transport/connection failure as a durable retry.
func WrapConnectionError(err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassConnection, 0, err)
}

// WrapTimeoutError marks a request timeout as a durable retry.
func WrapTimeoutError(err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassTimeout, 0, err)
}

// WrapServerError marks a 5xx provider error as a durable retry.
func WrapServerError(err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassServerError, 0, err)
}

// WrapRateLimitError marks a rate limit as a durable retry that honours the
// provider's retry-after hint.
func WrapRateLimitError(retryAfter time.Duration, err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassRateLimited, retryAfter, err)
}

// WrapInvalidOutputError marks malformed, truncated or otherwise invalid
// structured output. It counts against a small budget, then errors.
func WrapInvalidOutputError(err error) *ClassifiedError {
	return wrap(KindInvalidOutput, ErrorClassInvalidOutput, 0, err)
}

// WrapContentFilterError marks a provider refusal or content filter. No verdict
// is inferred; bounded recovery then errors.
func WrapContentFilterError(err error) *ClassifiedError {
	return wrap(KindContentFilter, ErrorClassContentFilter, 0, err)
}

// WrapConfigurationError marks bad credentials, an unsupported model or
// parameter. Workers release their claim and stop the pool until operators
// correct the runtime settings and restart the process.
func WrapConfigurationError(err error) *ClassifiedError {
	return wrap(KindConfiguration, ErrorClassConfiguration, 0, err)
}

// Classify is the single mapping from concrete failures to stored classes and retry handling.
func Classify(err error) (string, ErrorKind, time.Duration) {
	var ce *ClassifiedError
	if errors.As(err, &ce) {
		return ce.ErrorClass, ce.Kind, ce.RetryAfter
	}
	switch {
	case errors.Is(err, ErrEmptyResponse), errors.As(err, new(*ErrInvalidOutput)):
		return ErrorClassInvalidOutput, KindInvalidOutput, 0
	case errors.As(err, new(*ErrRefusal)):
		return ErrorClassContentFilter, KindContentFilter, 0
	case errors.As(err, new(*ErrRateLimited)):
		var rate *ErrRateLimited
		errors.As(err, &rate)
		return ErrorClassRateLimited, KindDurable, rate.RetryAfter
	case errors.As(err, new(*ErrTimeout)):
		return ErrorClassTimeout, KindDurable, 0
	case errors.As(err, new(*ErrConfiguration)):
		return ErrorClassConfiguration, KindConfiguration, 0
	case errors.As(err, new(*ErrConnection)), errors.As(err, new(*ErrContext)):
		return ErrorClassConnection, KindDurable, 0
	}
	return ErrorClassServerError, KindDurable, 0
}
