//go:build integration

// Uses fake HTTP servers; requires local socket access.
package openaiadapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	openai "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const sampleBody = `{"id":"chatcmpl-x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"{\"decision\":\"approve\",\"reason_codes\":[]}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

type captured struct {
	path  string
	auth  string
	body  string
	count int
}

func runServer(t *testing.T, status int, retryAfter string, body string) (*httptest.Server, *captured) {
	t.Helper()
	rec := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.count++
		rec.path = r.URL.Path
		rec.auth = r.Header.Get("Authorization")
		buf, _ := io.ReadAll(r.Body)
		rec.body = string(buf)
		if retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != "" {
			_, _ = fmt.Fprint(w, body)
		}
	}))
	return srv, rec
}

func testRequest() *moderation.CompletionRequest {
	return &moderation.CompletionRequest{
		Model:  "ornith-1.5",
		System: "moderation system prompt",
		User:   "user review content",
	}
}

func TestCompleteJSONObjectMode(t *testing.T) {
	srv, rec := runServer(t, http.StatusOK, "", sampleBody)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = adapter.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rec.path != "/chat/completions" || rec.auth != "Bearer sk-x" {
		t.Fatalf("provider request path=%q auth header present=%v", rec.path, rec.auth != "")
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(rec.body), &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	format := req["response_format"].(map[string]any)
	if req["model"] != "ornith-1.5" || len(req["messages"].([]any)) != 2 {
		t.Fatalf("unexpected model or conversation: %s", rec.body)
	}
	if format["type"] != "json_object" {
		t.Errorf("response_format type = %v, want json_object", format["type"])
	}
}

func TestCompleteOmittedOptionalParamsNotSent(t *testing.T) {
	srv, rec := runServer(t, http.StatusOK, "", sampleBody)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = adapter.Complete(context.Background(), testRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(rec.body), &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	for _, key := range []string{"temperature", "top_p", "presence_penalty", "frequency_penalty", "max_completion_tokens", "max_tokens", "seed", "stop", "reasoning_effort"} {
		if _, ok := req[key]; ok {
			t.Errorf("optional parameter %q must not be sent when unset", key)
		}
	}
}

func TestCompleteMaps429WithRetryAfter(t *testing.T) {
	srv, _ := runServer(
		t,
		http.StatusTooManyRequests,
		"30",
		`{"error":{"type":"rate_limit_error","message":"slow down"}}`,
	)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-super-secret", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = adapter.Complete(context.Background(), testRequest())
	var rateLimited *moderation.ErrRateLimited
	if !errors.As(err, &rateLimited) {
		t.Fatalf("error = %v, want *ErrRateLimited", err)
	}
	if class, _, _ := moderation.Classify(err); class != moderation.ErrorClassRateLimited {
		t.Error("rate limit must be retryable")
	}
	if !rateLimited.HasRetryAfter || rateLimited.RetryAfter != 30*time.Second {
		t.Errorf("retry-after = %v (has=%v), want 30s", rateLimited.RetryAfter, rateLimited.HasRetryAfter)
	}
	if strings.Contains(err.Error(), "sk-super-secret") {
		t.Errorf("error leaked the API key: %v", err)
	}
}

func TestCompleteMaps4xxConfiguration(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		srv, _ := runServer(t, status, "", `{"error":{"type":"invalid_request_error","message":"bad config"}}`)
		adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = adapter.Complete(context.Background(), testRequest())
		var configErr *moderation.ErrConfiguration
		if !errors.As(err, &configErr) {
			t.Errorf("status %d: error = %v, want *ErrConfiguration", status, err)
		}
		if class, _, _ := moderation.Classify(err); class != moderation.ErrorClassConfiguration {
			t.Errorf("status %d: configuration error must not be retryable", status)
		}
	}
}

func TestCompleteMaps5xxRetryable(t *testing.T) {
	for _, status := range []int{http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusBadGateway} {
		srv, _ := runServer(t, status, "", `{"error":{"type":"server_error","message":"boom"}}`)
		adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = adapter.Complete(context.Background(), testRequest())
		var connErr *moderation.ErrConnection
		if !errors.As(err, &connErr) {
			t.Errorf("status %d: error = %v, want *ErrConnection", status, err)
		}
		if class, _, _ := moderation.Classify(err); class != moderation.ErrorClassConnection {
			t.Errorf("status %d: 5xx must be retryable", status)
		}
	}
}

func TestCompleteDoesNotRetryOn429(t *testing.T) {
	srv, rec := runServer(
		t,
		http.StatusTooManyRequests,
		"1",
		`{"error":{"type":"rate_limit_error","message":"slow down"}}`,
	)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = adapter.Complete(context.Background(), testRequest())
	if rec.count != 1 {
		t.Errorf("requests = %d, want 1 (SDK retries disabled)", rec.count)
	}
}

func TestNewRejectsMissingKeyAndBadURL(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://x/v1"}); err == nil {
		t.Error("expected error for missing API key")
	}
	for _, baseURL := range []string{"://bad", "provider.example/v1", "/v1", "ftp://provider.example/v1", "https:///v1"} {
		if _, err := New(Config{BaseURL: baseURL, APIKey: "k"}); err == nil {
			t.Errorf("invalid base URL accepted: %q", baseURL)
		}
	}
}

func TestProviderEchoCannotLeakCredentialsOrReview(t *testing.T) {
	for _, status := range []int{400, 401, 429, 500} {
		secret := "sk-private-secret private-review private-prompt"
		body, _ := json.Marshal(map[string]any{"error": map[string]string{"type": secret, "message": secret}})
		srv, _ := runServer(t, status, "", string(body))
		adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-private-secret"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = adapter.Complete(context.Background(), testRequest())
		srv.Close()
		if err == nil {
			t.Fatal("expected provider error")
		}
		for _, value := range strings.Fields(secret) {
			if strings.Contains(err.Error(), value) {
				t.Fatalf("HTTP %d leaked %q: %v", status, value, err)
			}
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestConfiguredTimeoutAndShorterCallerDeadlineReachTransport(t *testing.T) {
	for _, callerLimit := range []time.Duration{0, 20 * time.Second} {
		t.Run(callerLimit.String(), func(t *testing.T) {
			adapter, err := New(
				Config{BaseURL: "https://provider.example/v1", APIKey: "fake", Timeout: 150 * time.Second},
			)
			if err != nil {
				t.Fatal(err)
			}
			transport := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				deadline, ok := r.Context().Deadline()
				if !ok {
					t.Fatal("transport has no deadline")
				}
				want := 150 * time.Second
				if callerLimit > 0 {
					want = callerLimit
				}
				remaining := time.Until(deadline)
				if remaining > want || remaining < want-time.Second {
					t.Errorf("transport deadline=%v, want %v", remaining, want)
				}
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(sampleBody)),
					Request:    r,
				}, nil
			})
			client := openai.NewClient(
				option.WithBaseURL("https://provider.example/v1"),
				option.WithAPIKey("fake"),
				option.WithMaxRetries(0),
				option.WithHTTPClient(&http.Client{Transport: transport}),
			)
			adapter.chatCompletions = client.Chat.Completions
			ctx := context.Background()
			if callerLimit > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, callerLimit)
				defer cancel()
			}
			req := testRequest()
			if _, err := adapter.Complete(ctx, req); err != nil {
				t.Fatal(err)
			}
		})
	}
}
