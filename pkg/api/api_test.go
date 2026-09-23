package api

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type serviceError struct{}

func (serviceError) Error() string      { return "service rejected request" }
func (serviceError) ErrorTrace() string { return "service rejected request" }
func (serviceError) Code() int          { return http.StatusUnauthorized }

func TestHTTPHandlerUsesConfiguredLoggerAndError(t *testing.T) {
	var logs bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	type env struct{ value string }
	handler := HTTPHandler(&env{value: "ready"}, func(_ *http.Request, e *env) (*Response, error) {
		if e.value != "ready" {
			t.Fatal("handler received wrong environment")
		}
		return nil, serviceError{}
	})

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "service rejected request") {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(logs.String(), "service rejected request") {
		t.Fatalf("service logger did not receive error: %s", logs.String())
	}
}

func TestDecodeErrorIsBadRequest(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not json"))
	var body struct{ Name string }
	err := Decode(r, &body)
	var external interface{ Code() int }
	if !errors.As(err, &external) || external.Code() != http.StatusBadRequest {
		t.Fatalf("expected 400 error, got %v", err)
	}
}

func TestHandlerTimeoutReturnsEnvelope(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	type env struct{}
	handler := httpHandler(&env{}, func(r *http.Request, _ *env) (*Response, error) {
		<-release // Deliberately ignores cancellation to check the response deadline.
		return NewResponse("late")
	}, 5*time.Millisecond)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "request timed out") {
		t.Fatalf("unexpected timeout response: %d %s", w.Code, w.Body.String())
	}
}

func TestStreamStartsBeforeReaderCompletes(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	type env struct{}
	handler := httpHandler(&env{}, func(_ *http.Request, _ *env) (*Response, error) {
		return NewStreamResponse(reader, "text/plain")
	}, time.Second)

	firstWrite := make(chan struct{}, 1)
	finished := make(chan struct{})
	w := &observedWriter{header: make(http.Header), firstWrite: firstWrite}
	go func() {
		defer close(finished)
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	}()
	if _, err := writer.Write([]byte("first")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstWrite:
	case <-time.After(time.Second):
		t.Fatal("stream did not write before reader completed")
	}
	writer.Close()
	<-finished
}

type observedWriter struct {
	header     http.Header
	firstWrite chan struct{}
}

func (w *observedWriter) Header() http.Header { return w.header }
func (w *observedWriter) WriteHeader(int)     {}
func (w *observedWriter) Write(p []byte) (int, error) {
	select {
	case w.firstWrite <- struct{}{}:
	default:
	}
	return len(p), nil
}
