package openaiadapter

import (
	"context"
	"errors"
	"fmt"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
	openai "github.com/openai/openai-go/v3"
	"net"
	"net/http"
	"strconv"
	"time"
)

// mapError converts an SDK or transport error into the appropriate llmpolicy
// error class, redacting credentials and bounding diagnostics.
func mapError(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return mapTransportError(err)
	}
	switch {
	case apiErr.StatusCode == http.StatusTooManyRequests:
		return rateLimitedError(apiErr)
	case apiErr.StatusCode >= 500:
		// Server-side errors are transient and retryable.
		return &llmpolicy.ErrConnection{Detail: classifyMessage(apiErr)}
	case apiErr.StatusCode >= 400:
		// 4xx indicate a configuration or invalid request; not retried.
		return &llmpolicy.ErrConfiguration{Detail: classifyMessage(apiErr)}
	default:
		return &llmpolicy.ErrConnection{Detail: fmt.Sprintf("unexpected provider status %d", apiErr.StatusCode)}
	}
}

// mapTransportError handles non-API errors (connection, DNS, TLS, timeout) that
// the SDK does not wrap in *openai.Error.
func mapTransportError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &llmpolicy.ErrTimeout{Detail: "request deadline exceeded"}
	case errors.Is(err, context.Canceled):
		return &llmpolicy.ErrContext{}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return &llmpolicy.ErrTimeout{Detail: "provider request timed out at the transport layer"}
		}
		return &llmpolicy.ErrConnection{Detail: "provider transport failed"}
	}
	return &llmpolicy.ErrConnection{Detail: "provider transport failed"}
}

// rateLimitedError builds an ErrRateLimited, honouring a Retry-After header when
// the provider advertises one.
func rateLimitedError(apiErr *openai.Error) *llmpolicy.ErrRateLimited {
	rlErr := &llmpolicy.ErrRateLimited{Detail: classifyMessage(apiErr)}
	if apiErr.Response != nil {
		if retryAfter, ok := parseRetryAfter(apiErr.Response.Header.Get("Retry-After")); ok {
			rlErr.RetryAfter = retryAfter
			rlErr.HasRetryAfter = true
		}
	}
	return rlErr
}

// parseRetryAfter parses a Retry-After value as either a delay in seconds or an
// HTTP date. It returns ok=false when the header is absent or unparsable.
func parseRetryAfter(value string) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if seconds, err := parseIntSeconds(value); err == nil {
		if seconds >= 0 {
			return time.Duration(seconds) * time.Second, true
		}
	}
	if t, err := time.Parse(http.TimeFormat, value); err == nil {
		delay := time.Until(t)
		if delay < 0 {
			delay = 0
		}
		return delay, true
	}
	return 0, false
}

// parseIntSeconds parses a non-negative integer number of seconds.
func parseIntSeconds(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, err
	}
	return n, nil
}

// classifyMessage renders a short, sanitized provider error summary. It never
// leaks the API key or request headers.
func classifyMessage(apiErr *openai.Error) string {
	switch apiErr.StatusCode {
	case 401, 403:
		return "provider rejected server credentials (HTTP 401/403)"
	case 400, 404, 422:
		return "provider rejected the selected model, parameters or output mode"
	default:
		return fmt.Sprintf("provider returned HTTP %d", apiErr.StatusCode)
	}
}
