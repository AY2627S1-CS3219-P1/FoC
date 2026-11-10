package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise the supported CLI rather than only calling renderer methods.
func TestCLIProducesOnlyPrivateOverlayAndRetainedDatabaseCredentials(t *testing.T) {
	a, directory, _ := testApplication(t)
	tool := buildTool(t)
	outputs := filepath.Join(directory, "outputs.json")
	ca := filepath.Join(directory, "rds-ca.pem")
	admin := filepath.Join(directory, "admin.json")
	if err := saveJSON(a.root, outputs, testConfiguration()); err != nil {
		t.Fatal(err)
	}
	if err := saveJSON(
		a.root,
		admin,
		map[string]any{"username": "offline_admin", "password": "cli-fixture-only-secret"},
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ca, []byte("-----BEGIN CERTIFICATE-----\nOFFLINE\n"), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, database := filepath.Join(directory, "overlay"), filepath.Join(directory, "database")
	run := func(args ...string) string {
		cmd := exec.Command(tool, args...)
		cmd.Dir = a.root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("CLI failed: %s", output)
		}
		if strings.Contains(string(output), "cli-fixture-only-secret") {
			t.Fatal("CLI printed administrator credential")
		}
		return string(output)
	}
	run("render-overlay", "--outputs", outputs, "--output", overlay, "--rds-ca", ca)
	if len(array(readTestObject(t, filepath.Join(overlay, "kustomization.yaml"))["resources"])) != 6 {
		t.Fatal("CLI did not render six stages")
	}
	for i := 0; i < 2; i++ {
		run("prepare-database", "--outputs", outputs, "--admin-secret", admin, "--directory", database)
	}
	if len(array(readTestObject(t, filepath.Join(database, "secrets.json"))["items"])) != 7 {
		t.Fatal("CLI did not prepare five database Secrets and the provisioning inputs")
	}
}
