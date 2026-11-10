package moderation

import (
	"errors"
	"fmt"
	"time"
)

// Provider and validation failures carry no content verdict.
// Diagnostic fields must contain sanitized summaries, never provider bodies or credentials.

// Empty content must never imply approval.
var ErrEmptyResponse = errors.New("moderation: provider returned no content")

// Output failed local schema or verdict validation.
type ErrInvalidOutput struct {
	// Detail is a short, human-readable explanation. It never contains the
	// evaluated content, the prompt, or provider diagnostics.
	Detail string
}

func (e *ErrInvalidOutput) Error() string {
	return "moderation: invalid policy output: " + e.Detail
}

// Refusal or provider filtering is a technical failure to assess.
type ErrRefusal struct {
	// Reason is a short, sanitized description of the refusal. It never
	// echoes the raw provider refusal verbatim.
	Reason string
}

func (e *ErrRefusal) Error() string {
	return "moderation: provider refused evaluation: " + e.Reason
}

// Honor a supplied Retry-After within the durable retry bound.
type ErrRateLimited struct {
	Detail string
	// HasRetryAfter distinguishes a missing header from an explicit zero.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *ErrRateLimited) Error() string {
	if e.HasRetryAfter {
		return fmt.Sprintf("moderation: provider rate limited the request (retry after %s): %s", e.RetryAfter, e.Detail)
	}
	return "moderation: provider rate limited the request: " + e.Detail
}

type ErrTimeout struct {
	Detail string
}

func (e *ErrTimeout) Error() string { return "moderation: evaluation timed out: " + e.Detail }

// Caller cancellation ends the current attempt without retrying in the adapter.
type ErrContext struct{}

func (e *ErrContext) Error() string { return "moderation: evaluation context was cancelled or expired" }

// Invalid settings stop the worker pool until restart.
type ErrConfiguration struct {
	Detail string
}

func (e *ErrConfiguration) Error() string {
	return "moderation: provider rejected the configuration or request: " + e.Detail
}

// Transport failures and HTTP 5xx use durable retry.
type ErrConnection struct {
	Detail string
}

func (e *ErrConnection) Error() string {
	return "moderation: failed to reach the provider: " + e.Detail
}
