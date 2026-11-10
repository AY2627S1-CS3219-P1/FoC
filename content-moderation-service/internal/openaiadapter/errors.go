package openaiadapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
)

// Map provider failures to sanitized moderation error classes.
func mapError(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return mapTransportError(err)
	}
	switch {
	case apiErr.StatusCode == http.StatusTooManyRequests:
		return rateLimitedError(apiErr)
	case apiErr.StatusCode >= 500:
		return &moderation.ErrConnection{Detail: classifyMessage(apiErr)}
	case apiErr.StatusCode >= 400:
		return &moderation.ErrConfiguration{Detail: classifyMessage(apiErr)}
	default:
		return &moderation.ErrConnection{Detail: fmt.Sprintf("unexpected provider status %d", apiErr.StatusCode)}
	}
}

// Handle transport errors that the SDK does not wrap as API errors.
func mapTransportError(err error) error {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &moderation.ErrTimeout{Detail: "request deadline exceeded"}
	case errors.Is(err, context.Canceled):
		return &moderation.ErrContext{}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return &moderation.ErrTimeout{Detail: "provider request timed out at the transport layer"}
		}
		return &moderation.ErrConnection{Detail: "provider transport failed"}
	}
	return &moderation.ErrConnection{Detail: "provider transport failed"}
}

func rateLimitedError(apiErr *openai.Error) *moderation.ErrRateLimited {
	rlErr := &moderation.ErrRateLimited{Detail: classifyMessage(apiErr)}
	if apiErr.Response != nil {
		if retryAfter, ok := parseRetryAfter(apiErr.Response.Header.Get("Retry-After")); ok {
			rlErr.RetryAfter = retryAfter
			rlErr.HasRetryAfter = true
		}
	}
	return rlErr
}

// Retry-After accepts integer seconds or an HTTP date.
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

func parseIntSeconds(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, err
	}
	return n, nil
}

// Exclude keys, headers and raw provider bodies from persisted diagnostics.
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
