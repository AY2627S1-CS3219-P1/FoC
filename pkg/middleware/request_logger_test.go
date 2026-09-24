package middleware

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

func TestRequestLoggerRecordsStartAndCompletion(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		level  string
	}{
		{name: "success", status: http.StatusOK, level: "INFO"},
		{name: "client error", status: http.StatusUnauthorized, level: "WARN"},
		{name: "server error", status: http.StatusInternalServerError, level: "ERROR"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, nil)))
			t.Cleanup(func() { slog.SetDefault(previous) })

			handler := chimiddleware.RequestID(RequestLogger(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(test.status)
			})))
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/health", nil))

			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("expected start and completion logs, got %d: %s", len(lines), logs.String())
			}
			var start, completed map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &start); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(lines[1]), &completed); err != nil {
				t.Fatal(err)
			}
			id, ok := start["request_id"].(string)
			if start["msg"] != "request started" || start["method"] != http.MethodGet || start["path"] != "/health" || !ok || id == "" {
				t.Fatalf("unexpected start log: %v", start)
			}
			if completed["msg"] != "request completed" || completed["level"] != test.level || completed["status"] != float64(test.status) || completed["request_id"] != start["request_id"] {
				t.Fatalf("unexpected completion log: %v", completed)
			}
		})
	}
}
