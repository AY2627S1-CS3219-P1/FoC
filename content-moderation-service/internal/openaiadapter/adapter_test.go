//go:build integration

// Network tests exercise the adapter against a fake OpenAI-compatible server.
// They are gated behind the "integration" tag because they open a local socket
// via httptest; run them with `-tags integration`. In a sandbox that denies
// local sockets, skip this tag.
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

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/llmpolicy"
)

const sampleBody = `{"id":"chatcmpl-x","object":"chat.completion","created":0,"model":"m","choices":[{"index":0,"finish_reason":"stop","logprobs":null,"message":{"role":"assistant","content":"{\"decision\":\"approve\",\"reason_codes\":[]}"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`

// captured records the request a test server received.
type captured struct {
	path  string
	auth  string
	body  string
	count int
}

// runServer starts a handler that records the request and writes the given
// status, optional Retry-After, and body.
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
			fmt.Fprint(w, body)
		}
	}))
	return srv, rec
}

// testRequest is the baseline completion request.
func testRequest(outputMode string) *llmpolicy.CompletionRequest {
	return &llmpolicy.CompletionRequest{
		Model:      "ornith-1.5",
		System:     "moderation system prompt",
		User:       "user review content",
		OutputMode: outputMode,
		SchemaJSON: llmpolicy.SchemaJSON,
		Timeout:    5 * time.Second,
	}
}

func TestCompleteValidApproveJSONSchema(t *testing.T) {
	srv, rec := runServer(t, http.StatusOK, "", sampleBody)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL + "/v1", APIKey: "sk-super-secret", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONSchema)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp == nil || resp.Content != `{"decision":"approve","reason_codes":[]}` {
		t.Errorf("resp = %+v", resp)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("finish reason = %q", resp.FinishReason)
	}
	// The provider completion ID must never be copied into the response.
	if resp.Model == "chatcmpl-x" {
		t.Errorf("copied an LLM-generated identity into the response model")
	}

	if rec.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", rec.path)
	}
	if rec.auth != "Bearer sk-super-secret" {
		t.Errorf("authorization = %q, want bearer token", rec.auth)
	}

	var req map[string]any
	if err := json.Unmarshal([]byte(rec.body), &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if req["model"] != "ornith-1.5" {
		t.Errorf("model = %v, want ornith-1.5", req["model"])
	}
	format := req["response_format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Errorf("response_format type = %v, want json_schema", format["type"])
	}
	js := format["json_schema"].(map[string]any)
	if js["strict"] != true {
		t.Errorf("strict = %v, want true", js["strict"])
	}
	messages := req["messages"].([]any)
	if len(messages) != 2 {
		t.Fatalf("messages = %d, want 2 (fresh single-turn)", len(messages))
	}
}

func TestCompleteJSONObjectMode(t *testing.T) {
	srv, rec := runServer(t, http.StatusOK, "", sampleBody)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONObject)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(rec.body), &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	format := req["response_format"].(map[string]any)
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
	_, err = adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONSchema)))
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

func TestCompleteSetsOptionalParamsWhenPresent(t *testing.T) {
	srv, rec := runServer(t, http.StatusOK, "", sampleBody)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	temperature := 0.2
	_, err = adapter.Complete(context.Background(), func() *llmpolicy.CompletionRequest {
		req := testRequest(string(llmpolicy.OutputModeJSONSchema))
		req.Parameters = llmpolicy.CompletionParameters{Temperature: &temperature, Stop: []string{"END"}}
		return req
	}())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var req map[string]any
	if err := json.Unmarshal([]byte(rec.body), &req); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if req["temperature"] != 0.2 {
		t.Errorf("temperature = %v, want 0.2", req["temperature"])
	}
	if _, ok := req["stop"]; !ok {
		t.Error("stop must be sent when set")
	}
}

func TestCompleteMaps429WithRetryAfter(t *testing.T) {
	srv, _ := runServer(t, http.StatusTooManyRequests, "30", `{"error":{"type":"rate_limit_error","message":"slow down"}}`)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-super-secret", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONSchema)))
	var rateLimited *llmpolicy.ErrRateLimited
	if !errors.As(err, &rateLimited) {
		t.Fatalf("error = %v, want *ErrRateLimited", err)
	}
	if !rateLimited.IsRetryable() {
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
		_, err = adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONSchema)))
		var configErr *llmpolicy.ErrConfiguration
		if !errors.As(err, &configErr) {
			t.Errorf("status %d: error = %v, want *ErrConfiguration", status, err)
		}
		if configErr.IsRetryable() {
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
		_, err = adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONSchema)))
		var connErr *llmpolicy.ErrConnection
		if !errors.As(err, &connErr) {
			t.Errorf("status %d: error = %v, want *ErrConnection", status, err)
		}
		if !connErr.IsRetryable() {
			t.Errorf("status %d: 5xx must be retryable", status)
		}
	}
}

func TestCompleteDoesNotRetryOn429(t *testing.T) {
	srv, rec := runServer(t, http.StatusTooManyRequests, "1", `{"error":{"type":"rate_limit_error","message":"slow down"}}`)
	defer srv.Close()

	adapter, err := New(Config{BaseURL: srv.URL, APIKey: "sk-x", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, _ = adapter.Complete(context.Background(), testRequest(string(llmpolicy.OutputModeJSONSchema)))
	if rec.count != 1 {
		t.Errorf("requests = %d, want 1 (SDK retries disabled)", rec.count)
	}
}

func TestNewRejectsMissingKeyAndBadURL(t *testing.T) {
	if _, err := New(Config{BaseURL: "http://x/v1"}); err == nil {
		t.Error("expected error for missing API key")
	}
	if _, err := New(Config{BaseURL: "://bad", APIKey: "k"}); err == nil {
		t.Error("expected error for invalid base URL")
	}
}
