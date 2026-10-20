package database

import (
	"io/fs"
	"strings"
	"testing"
)

func TestMigrationBundleIncludesCompleteSupplierSchema(t *testing.T) {
	files, err := fs.Glob(migrations, "schema/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 10 {
		t.Fatalf("embedded %d migrations, expected all 10", len(files))
	}
	for _, name := range files {
		body, err := migrations.ReadFile(name)
		if err != nil || !strings.Contains(string(body), "-- +goose Up") || !strings.Contains(string(body), "-- +goose Down") {
			t.Fatalf("invalid embedded migration %s: %v", name, err)
		}
	}
}
