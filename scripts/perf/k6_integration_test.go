package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// An isolated TLS server verifies the actual k6 runtime, cookie rotation,
// response assertions and drain. This is harness validation, not AWS capacity.
func TestK6RuntimeIntegration(t *testing.T) {
	binary := os.Getenv("TEST_K6_BINARY")
	if binary == "" {
		t.Skip("TEST_K6_BINARY is not set")
	}
	var mu sync.Mutex
	cookies := map[string]string{"user": "initial"}
	methods := map[string]int{}
	badCookies := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Millisecond)
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		mu.Lock()
		defer mu.Unlock()
		methods[method]++
		w.Header().Set("Content-Type", "application/json")
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid", 400)
			return
		}
		var response any
		switch method {
		case "Refresh":
			cookie, err := r.Cookie(refreshCookie)
			if err != nil || cookie.Value != cookies["user"] {
				badCookies++
				http.Error(w, "unauthenticated", 401)
				return
			}
			cookies["user"] = fmt.Sprint("rotated-", methods[method])
			http.SetCookie(
				w,
				&http.Cookie{Name: refreshCookie, Value: cookies["user"], Path: authPath, Secure: true, HttpOnly: true},
			)
			response = map[string]any{"accessToken": "signed", "user": map[string]string{"id": "user"}}
		case "GetMyProfile", "UpdateMyProfile":
			response = map[string]any{"profile": map[string]string{"id": "user"}}
		case "GetMyAccount":
			response = map[string]any{"account": map[string]string{"userId": "user", "balance": "1000000"}}
		case "ListOpenOrders", "ListMyOrders", "ListMyLedgerEntries":
			response = map[string]any{"pageInfo": map[string]int{"page": 1}}
		case "ListLocations":
			response = map[string]int{"page": 1, "pageSize": 20}
		case "GetOrder":
			response = map[string]any{"order": map[string]any{"id": body["id"], "revision": "2"}}
		case "CreateOrder":
			response = map[string]any{
				"order": map[string]string{"id": "created", "revision": "2", "status": "ORDER_STATUS_AWAITING_COURIER"},
			}
		case "CancelOrder":
			response = map[string]any{"order": map[string]any{"id": body["id"], "status": "ORDER_STATUS_CANCELLED"}}
		default:
			http.Error(w, "unimplemented", 404)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := saveJSON(filepath.Join(dir, "sessions.json"), []session{{"user", "signed", "initial"}}); err != nil {
		t.Fatal(err)
	}
	if err := saveJSON(
		filepath.Join(dir, "fixtures.json"),
		map[string]any{"locationIds": []string{"pickup", "delivery"}, "openIds": []string{"open"}},
	); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "run", "--out", "json="+filepath.Join(dir, "raw.json"), "api.js")
	cmd.Env = append(os.Environ(), "PERF_BASE_URL="+server.URL, "PERF_VUS=1", "PERF_ACCOUNTS_PER_VU=1",
		"PERF_WARMUP_SECONDS=1", "PERF_MEASURE_SECONDS=2", "K6_INSECURE_SKIP_TLS_VERIFY=true",
		"PERF_FIXTURES="+filepath.Join(dir, "fixtures.json"), "PERF_SESSIONS="+filepath.Join(dir, "sessions.json"),
		"PERF_SUMMARY="+filepath.Join(dir, "summary.json"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("k6 runtime: %v\n%s", err, output)
	}
	if badCookies != 0 || methods["Refresh"] < 2 || methods["CreateOrder"] == 0 || methods["CancelOrder"] == 0 {
		t.Fatalf("missing workflow/cookie coverage: cookies=%d methods=%v", badCookies, methods)
	}
	var summary struct {
		Metrics map[string]struct {
			Values map[string]float64 `json:"values"`
		} `json:"metrics"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatal(err)
	}
	metric, exists := summary.Metrics["attempt_failed"]
	if !exists || metric.Values["rate"] != 0 {
		t.Fatal("k6 recorded unexpected failures")
	}
}
