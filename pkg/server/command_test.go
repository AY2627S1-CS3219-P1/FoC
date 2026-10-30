package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestProbeChecksOnlyRequiredDependencies(t *testing.T) {
	for _, tc := range []struct {
		name            string
		internal        bool
		command         Command
		internalMissing bool
		databaseDown    bool
		healthProcedure string
		wantPaths       string
		wantError       bool
	}{
		{name: "external live", command: Live, databaseDown: true, wantPaths: "8080/livez"},
		{name: "external live ignores dependencies", command: Live, internalMissing: true, databaseDown: true, wantPaths: "8080/livez"},
		{name: "external ready", command: Ready, healthProcedure: "/health/Check", wantPaths: "8080/health/Check"},
		{name: "internal ready", internal: true, command: Ready, healthProcedure: "/grpc.health.v1.Health/Check", wantPaths: "8081/livez,8080/grpc.health.v1.Health/Check"},
		{name: "database failure", command: Ready, healthProcedure: "/health/Check", databaseDown: true, wantPaths: "8080/health/Check", wantError: true},
		{name: "missing internal listener", internal: true, command: Ready, internalMissing: true, wantPaths: "8081/livez", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.Hostname() != "127.0.0.1" {
					t.Fatalf("probe used non-local host: %s", request.URL.Host)
				}
				requests = append(requests, request.URL.Port()+request.URL.Path)
				if request.URL.Port() == "8081" && tc.internalMissing {
					return nil, errors.New("connection refused")
				}
				status, body := http.StatusOK, ""
				if request.URL.Path != "/livez" {
					if request.Method != http.MethodPost || request.Header.Get("Connect-Protocol-Version") != "1" || request.Header.Get("Content-Type") != "application/json" {
						t.Fatal("readiness is not a Connect RPC")
					}
					payload, err := io.ReadAll(request.Body)
					if err != nil || string(payload) != "{}" {
						t.Fatalf("payload=%s err=%v", payload, err)
					}
					body = `{"status":"ok"}`
					if tc.healthProcedure == "/grpc.health.v1.Health/Check" {
						body = `{"status":"SERVING"}`
					}
					if tc.databaseDown {
						status, body = http.StatusServiceUnavailable, `{"code":"unavailable"}`
					}
				} else if request.Method != http.MethodGet {
					t.Fatal("liveness should use GET")
				}
				return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
			})}
			cfg := Config{ExternalPort: "8080"}
			if tc.internal {
				cfg.InternalPort = "8081"
			}
			err := probe(context.Background(), client, tc.command, cfg, tc.healthProcedure)
			if (err != nil) != tc.wantError || strings.Join(requests, ",") != tc.wantPaths {
				t.Fatalf("requests=%v err=%v", requests, err)
			}
		})
	}
}

func TestProbeRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
	}{
		{"redirect", http.StatusTemporaryRedirect, ""},
		{"unavailable", http.StatusServiceUnavailable, ""},
		{"wrong status", http.StatusOK, `{"status":"starting"}`},
		{"empty response", http.StatusOK, ""},
		{"invalid response", http.StatusOK, "not JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/redirected")
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			parsed, err := url.Parse(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			cfg := Config{ExternalPort: parsed.Port()}
			if err := Probe(context.Background(), Ready, cfg, "/health/Check"); err == nil {
				t.Fatal("accepted failed readiness")
			}
		})
	}
}

func TestExecuteCommandLoadsOnlyNeededConfig(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		handled bool
		migrate bool
	}{
		{nil, false, false},
		{[]string{"migrate"}, true, true},
	} {
		called := false
		handled, err := ExecuteCommand(context.Background(), tc.args, func(key string) string {
			if key != "DATABASE_URL" {
				t.Fatalf("command read unrelated %s", key)
			}
			return "postgresql://migration-db"
		}, "unused", func(_ context.Context, dsn string) error {
			called = true
			if dsn != "postgresql://migration-db" {
				t.Fatal("wrong migration database")
			}
			return nil
		})
		if err != nil || handled != tc.handled || called != tc.migrate {
			t.Fatalf("handled=%t called=%t err=%v", handled, called, err)
		}
	}
	want := errors.New("migration failed")
	handled, err := ExecuteCommand(context.Background(), []string{"migrate"}, func(string) string { return "database" }, "", func(context.Context, string) error { return want })
	if !handled || !errors.Is(err, want) {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	_, err = ExecuteCommand(context.Background(), []string{"migrate"}, func(string) string { return "" }, "", func(context.Context, string) error {
		t.Fatal("migration called without database URL")
		return nil
	})
	if err == nil {
		t.Fatal("missing database URL accepted")
	}
}

func TestProbeCommandNeedsOnlyTransportConfig(t *testing.T) {
	server := httptest.NewServer(WithLiveness(http.NotFoundHandler()))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	handled, err := ExecuteCommand(context.Background(), []string{"probe", "live"}, func(key string) string {
		switch key {
		case "EXTERNAL_PORT":
			return parsed.Port()
		default:
			t.Fatalf("probe read unrelated %s", key)
			return ""
		}
	}, "unused", nil)
	if !handled || err != nil {
		t.Fatalf("handled=%t err=%v", handled, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Probe(ctx, Live, Config{ExternalPort: parsed.Port()}, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled probe err=%v", err)
	}
}
