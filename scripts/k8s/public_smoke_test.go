package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The existing gateway contract requires frontend/health success and 404 for
// private RPCs. Releases must check it through trusted public HTTPS, not a
// local forward or cluster-mutating mesh probe.
func TestPublicSmokeUsesTrustedEdgeAndSharedHealthContract(t *testing.T) {
	op := testOperator(t)
	log := filepath.Join(t.TempDir(), "curl-calls")
	t.Setenv("K8S_PUBLIC_ORIGIN", "https://release.example.test")
	t.Setenv("SMOKE_CALLS", log)
	fakeCommand(t, "curl", `printf '%s\n' "$*" | tr '\n' ' ' >> "$SMOKE_CALLS"
printf '\n' >> "$SMOKE_CALLS"
case "$*" in *--insecure*|*--cacert*|*localhost*) exit 90;; esac
for url do :; done
case "$url" in
  */GetPublicKeys|*/Reserve|*/GetUsers) printf '{}\n404';;
  */Check) printf '{"status":"SERVING"}\n200';;
  https://release.example.test/) printf '<html>FoC</html>\n200';;
  *) exit 91;;
esac`)
	if err := op.publicSmoke(); err != nil {
		t.Fatal(err)
	}
	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	if len(lines) != 1+len(publicHealthPaths)+len(publicSmokePrivatePaths) {
		t.Fatalf("expected frontend, all public health and all private-route requests, got %d", len(lines))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "--disable --silent --show-error") {
			t.Fatalf("hidden curl configuration was not disabled: %s", line)
		}
	}
	for _, path := range publicHealthPaths {
		if !strings.Contains(string(calls), "https://release.example.test"+path) {
			t.Fatalf("missing public health RPC: %s", path)
		}
	}
	for _, path := range publicSmokePrivatePaths {
		if !strings.Contains(string(calls), "https://release.example.test"+path) {
			t.Fatalf("missing private-route RPC: %s", path)
		}
	}
}

func TestPublicSmokeRejectsUnsafeOriginsAndFailedResponses(t *testing.T) {
	for _, origin := range []string{
		"", "http://release.example.test", "https://user:password@release.example.test",
		"https://release.example.test/path", "https://release.example.test?token=value",
		"https://release.example.test#fragment",
	} {
		t.Run(origin, func(t *testing.T) {
			op := testOperator(t)
			t.Setenv("K8S_PUBLIC_ORIGIN", origin)
			fakeCommand(t, "curl", "exit 99")
			if err := op.publicSmoke(); err == nil || !strings.Contains(err.Error(), "HTTPS origin") {
				t.Fatalf("unsafe origin: %v", err)
			}
		})
	}
	for name, response := range map[string]string{
		"unhealthy":    `{"status":"down"}\n200`,
		"http-failure": `{}\n503`,
		"invalid-json": `not-json\n200`,
	} {
		t.Run(name, func(t *testing.T) {
			op := testOperator(t)
			t.Setenv("K8S_PUBLIC_ORIGIN", "https://release.example.test")
			fakeCommand(t, "curl", "printf '"+response+"'")
			if err := op.publicSmoke(); err == nil {
				t.Fatal("failed public smoke was accepted")
			}
		})
	}
}
