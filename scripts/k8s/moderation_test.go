package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderKeyCanBeInstalledAfterKeylessDeployment(t *testing.T) {
	directory := t.TempDir()
	for _, value := range []string{"", "", "test-provider-key", "different-key"} {
		if err := persistValue(directory, "CMS_LLM_API_KEY", value, true); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(directory, "CMS_LLM_API_KEY")
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "test-provider-key" {
		t.Fatalf("provider key was not installed/preserved: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("provider key permissions: %v", err)
	}
	if err := persistValue(directory, "postgres-password", "original-password", false); err != nil {
		t.Fatal(err)
	}
	if err := persistValue(directory, "postgres-password", "replacement-password", false); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(directory, "postgres-password"))
	if err != nil || string(got) != "original-password" {
		t.Fatal("database password changed")
	}
	if err := os.WriteFile(filepath.Join(directory, "user-password"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := persistValue(directory, "user-password", "replacement", false); err == nil {
		t.Fatal("empty database password was silently replaced")
	}
}

func TestModerationGatewaySmokeUsesItsOwnHealthAlias(t *testing.T) {
	op := testOperator(t)
	fakeCommand(t, "curl", `case "$*" in
*https://localhost:18443/moderation/grpc.health.v1.Health/Check) printf '{"status":"SERVING"}\n200';;
*) printf '{"status":"NOT_SERVING"}\n503';;
esac`)
	forward := forwardSpec{localPort: 18443}
	if err := op.assertPublicHealth(forward, moderationGatewayHealthPath); err != nil {
		t.Fatal(err)
	}
	if err := op.assertPublicHealth(
		forward,
		"/grpc.health.v1.Health/Check",
	); err == nil ||
		!strings.Contains(err.Error(), "503") {
		t.Fatalf("wrong service's health passed: %v", err)
	}
}
