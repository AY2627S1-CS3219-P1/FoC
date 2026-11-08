package llmpolicy

import (
	"errors"
	"fmt"
	"time"
)

// Evaluation error classes. Every error returned from Evaluator.Evaluate is one
// of these: it is a technical outcome (a verdict was never obtained), never a
// content judgement. Callers must not read a verdict out of an error. The
// persistent job engine uses [Retryable] to decide whether to schedule another
// attempt.

// ErrEmptyResponse is returned when the provider produced no content at all
// (neither text nor a refusal). A missing response must never be read as
// approval.
var ErrEmptyResponse = errors.New("llmpolicy: provider returned no content")

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
	return "llmpolicy: invalid policy output: " + e.Detail
}

// IsRetryable reports that a malformed model response is not worth retrying; it
// indicates a prompt/schema problem, not a transient failure.
func (e *ErrInvalidOutput) IsRetryable() bool { return false }

// ErrRefusal is returned when the provider refused to evaluate the content. The
// refusal carries no verdict; callers must not infer approval or rejection from
// it.
type ErrRefusal struct {
	// Reason is a short, sanitized description of the refusal. It never
	// echoes the raw provider refusal verbatim.
	Reason string
}

func (e *ErrRefusal) Error() string {
	return "llmpolicy: provider refused evaluation: " + e.Reason
}

// IsRetryable reports that a refusal is not worth retrying.
func (e *ErrRefusal) IsRetryable() bool { return false }

// ErrRateLimited is returned when the provider rejected the request as a rate
// limit (HTTP 429). RetryAfter is populated when the provider advertises a
// Retry-After header.
type ErrRateLimited struct {
	// Detail is a short, sanitized explanation.
	Detail string
	// RetryAfter is the provider-advertised delay; HasRetryAfter reports whether
	// it was provided.
	RetryAfter  time.Duration
	HasRetryAfter bool
}

func (e *ErrRateLimited) Error() string {
	if e.HasRetryAfter {
		return fmt.Sprintf("llmpolicy: provider rate limited the request (retry after %s): %s", e.RetryAfter, e.Detail)
	}
	return "llmpolicy: provider rate limited the request: " + e.Detail
}

// IsRetryable reports that a rate limit is the canonical retryable failure.
func (e *ErrRateLimited) IsRetryable() bool { return true }

// ErrTimeout is returned when the request exceeded its deadline.
type ErrTimeout struct {
	// Detail is a short, sanitized explanation.
	Detail string
}

func (e *ErrTimeout) Error() string { return "llmpolicy: evaluation timed out: " + e.Detail }

// IsRetryable reports that a timeout is transient and retryable.
func (e *ErrTimeout) IsRetryable() bool { return true }

// ErrContext is returned when the caller's context was cancelled or expired
// before the request deadline. It is not retried: the caller asked us to stop.
type ErrContext struct{}

func (e *ErrContext) Error() string { return "llmpolicy: evaluation context was cancelled or expired" }

// IsRetryable reports that a caller-driven cancellation must not be retried.
func (e *ErrContext) IsRetryable() bool { return false }

// ErrConfiguration is returned for provider configuration or invalid-request
// errors (HTTP 4xx such as 400/401/403/404/422). These are not retried.
type ErrConfiguration struct {
	// Detail is a short, sanitized explanation. It must never leak the API key
	// or other request credentials.
	Detail string
}

func (e *ErrConfiguration) Error() string { return "llmpolicy: provider rejected the configuration or request: " + e.Detail }

// IsRetryable reports that a configuration error is not worth retrying.
func (e *ErrConfiguration) IsRetryable() bool { return false }

// ErrConnection is returned for transport-level failures (connection refused,
// DNS, TLS) and for server-side errors (HTTP 5xx). Both are transient and
// retryable.
type ErrConnection struct {
	// Detail is a short, sanitized explanation. It must never leak the API key
	// or request URLs.
	Detail string
}

func (e *ErrConnection) Error() string { return "llmpolicy: failed to reach the provider: " + e.Detail }

// IsRetryable reports that a transport or server error is transient.
func (e *ErrConnection) IsRetryable() bool { return true }

// retryable is implemented by error classes the job engine may retry.
type retryable interface{ IsRetryable() bool }

// Retryable reports whether the job engine should schedule another attempt for
// err according to its persistent retry policy. It is the stable contract the
// engine matches against the error classes defined here.
func Retryable(err error) bool {
	var r retryable
	if errors.As(err, &r) {
		return r.IsRetryable()
	}
	return false
}
