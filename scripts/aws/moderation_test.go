package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The extraction requires its own database; the existing AWS contract requires
// service-specific credentials, verified TLS, and idempotent credential reuse.
func TestModerationDatabaseOptInPreservesExistingCredentials(t *testing.T) {
	a, directory, output := testApplication(t)
	config := testConfiguration()
	admin := map[string]any{"username": "foc_admin", "password": "offline-test-only"}
	if err := a.prepareDatabase(config, admin, directory); err != nil {
		t.Fatal(err)
	}
	original := map[string][]byte{}
	for _, service := range []string{"user", "supplier", "order", "credit", "communication"} {
		data, err := os.ReadFile(filepath.Join(directory, service+".json"))
		if err != nil {
			t.Fatal(err)
		}
		original[service] = data
	}
	if _, err := os.Stat(filepath.Join(directory, "moderation.json")); !os.IsNotExist(err) {
		t.Fatal("moderation credentials must be opt-in")
	}
	config["moderation_enabled"] = true
	for attempt := 0; attempt < 2; attempt++ {
		if err := a.prepareDatabase(config, admin, directory); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(filepath.Join(directory, "moderation.json"))
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			original["moderation"] = data
		}
		for service, expected := range original {
			actual, err := os.ReadFile(filepath.Join(directory, service+".json"))
			if err != nil || !bytes.Equal(expected, actual) {
				t.Fatalf("retry rotated %s credentials", service)
			}
		}
	}
	credentials := readTestObject(t, filepath.Join(directory, "moderation.json"))
	if credentials["DB_NAME"] != "moderation_db" || credentials["DB_USER"] != "foc_moderation" ||
		!strings.Contains(stringValue(credentials, "DATABASE_URL"), "sslmode=verify-full") {
		t.Fatal("moderation requires its own database login and verified TLS")
	}
	if strings.Contains(output.String(), stringValue(credentials, "DB_PASSWORD")) {
		t.Fatal("moderation password appeared in operator output")
	}
	secrets := readTestObject(t, filepath.Join(directory, "secrets.json"))
	count := 0
	for _, item := range array(secrets["items"]) {
		if strings.HasSuffix(stringValue(object(object(item)["metadata"]), "name"), "-db") {
			count++
		}
	}
	if count != 6 {
		t.Fatalf("got %d service Secrets, want six", count)
	}
}

func TestModerationDatabaseRejectsNonBooleanFlag(t *testing.T) {
	a, directory, _ := testApplication(t)
	config := testConfiguration()
	config["moderation_enabled"] = "true"
	err := a.prepareDatabase(
		config,
		map[string]any{"username": "foc_admin", "password": "offline-test-only"},
		directory,
	)
	if err == nil || !strings.Contains(err.Error(), "must be a boolean") {
		t.Fatal("invalid opt-in must not silently omit the moderation database")
	}
}
