package moderation

import (
	"errors"
	"fmt"
	"time"
)

// These errors describe provider and response-validation failures. Classify
// determines their durable retry handling; none carries a content verdict.

// ErrEmptyResponse is returned when the provider produced no content at all
// (neither text nor a refusal). A missing response must never be read as
// approval.
var ErrEmptyResponse = errors.New("moderation: provider returned no content")

// ErrInvalidOutput is returned when the provider content was present but did not
// satisfy the policy schema: malformed JSON, trailing garbage, a missing or
// invalid decision, unknown/duplicate/oversized reason codes, or an
// approve-with-codes / reject-without-codes inconsistency.
type ErrInvalidOutput struct {
	// Detail is a short, human-readable explanation. It never contains the
	// evaluated content, the prompt, or provider diagnostics.
	Detail string
}

func (e *ErrInvalidOutput) Error() string {
	return "moderation: invalid policy output: " + e.Detail
}

// ErrRefusal means the provider did not produce an assessment, either because
// it returned a refusal or filtered the response. This is a technical failure,
// not a moderation verdict.
type ErrRefusal struct {
	// Reason is a short, sanitized description of the refusal. It never
	// echoes the raw provider refusal verbatim.
	Reason string
}

func (e *ErrRefusal) Error() string {
	return "moderation: provider refused evaluation: " + e.Reason
}

// ErrRateLimited is returned when the provider rejected the request as a rate
// limit (HTTP 429). RetryAfter is populated when the provider advertises a
// Retry-After header.
type ErrRateLimited struct {
	// Detail is a short, sanitized explanation.
	Detail string
	// RetryAfter is the provider-advertised delay; HasRetryAfter reports whether
	// it was provided.
	RetryAfter    time.Duration
	HasRetryAfter bool
}

func (e *ErrRateLimited) Error() string {
	if e.HasRetryAfter {
		return fmt.Sprintf("moderation: provider rate limited the request (retry after %s): %s", e.RetryAfter, e.Detail)
	}
	return "moderation: provider rate limited the request: " + e.Detail
}

// ErrTimeout is returned when the request exceeded its deadline.
type ErrTimeout struct {
	// Detail is a short, sanitized explanation.
	Detail string
}

func (e *ErrTimeout) Error() string { return "moderation: evaluation timed out: " + e.Detail }

// ErrContext is returned when the caller's context was cancelled or expired
// before the request deadline. It is not retried: the caller asked us to stop.
type ErrContext struct{}

func (e *ErrContext) Error() string { return "moderation: evaluation context was cancelled or expired" }

// ErrConfiguration is returned for provider configuration or invalid-request
// errors (HTTP 4xx such as 400/401/403/404/422). These are not retried.
type ErrConfiguration struct {
	// Detail is a short, sanitized explanation. It must never leak the API key
	// or other request credentials.
	Detail string
}

func (e *ErrConfiguration) Error() string {
	return "moderation: provider rejected the configuration or request: " + e.Detail
}

// ErrConnection is returned for transport-level failures (connection refused,
// DNS, TLS) and for server-side errors (HTTP 5xx). Both are transient and
// retryable.
type ErrConnection struct {
	// Detail is a short, sanitized explanation. It must never leak the API key
	// or request URLs.
	Detail string
}

func (e *ErrConnection) Error() string {
	return "moderation: failed to reach the provider: " + e.Detail
}
