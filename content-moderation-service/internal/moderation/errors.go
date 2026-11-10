// Package moderation assesses review content using a fixed policy.
package moderation

import (
	"errors"
	"time"
)

// Stored classes are sanitized; never include provider diagnostics.
const (
	ErrorClassConnection    = "connection"
	ErrorClassTimeout       = "timeout"
	ErrorClassServerError   = "server_error"
	ErrorClassRateLimited   = "rate_limited"
	ErrorClassInvalidOutput = "invalid_output"
	// Provider refusal is a technical failure to assess, not a content verdict.
	ErrorClassContentFilter = "content_filter"
	ErrorClassConfiguration = "configuration"
)

// ErrorKind selects retry handling; multiple stored classes may share one kind.
type ErrorKind string

const (
	KindDurable       ErrorKind = "durable"
	KindInvalidOutput ErrorKind = "invalid_output"
	KindContentFilter ErrorKind = "content_filter"
	KindConfiguration ErrorKind = "configuration"
)

// ClassifiedError preserves the cause while attaching retry metadata.
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

func WrapConnectionError(err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassConnection, 0, err)
}

func WrapTimeoutError(err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassTimeout, 0, err)
}

func WrapServerError(err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassServerError, 0, err)
}

func WrapRateLimitError(retryAfter time.Duration, err error) *ClassifiedError {
	return wrap(KindDurable, ErrorClassRateLimited, retryAfter, err)
}

func WrapInvalidOutputError(err error) *ClassifiedError {
	return wrap(KindInvalidOutput, ErrorClassInvalidOutput, 0, err)
}

func WrapContentFilterError(err error) *ClassifiedError {
	return wrap(KindContentFilter, ErrorClassContentFilter, 0, err)
}

// Workers release rejected settings and stop until operators correct settings and restart.
func WrapConfigurationError(err error) *ClassifiedError {
	return wrap(KindConfiguration, ErrorClassConfiguration, 0, err)
}

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
