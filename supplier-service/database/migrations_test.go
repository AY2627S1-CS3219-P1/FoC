package database

import (
	"io/fs"
	"os"
	"strings"
	"testing"
)

func TestMigrationDirectoryIncludesCompleteSupplierSchema(t *testing.T) {
	migrations := os.DirFS(".")
	files, err := fs.Glob(migrations, "schema/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatalf("migration directory has %d SQL files", len(files))
	}
	for _, name := range files {
		body, err := fs.ReadFile(migrations, name)
		if err != nil || !strings.Contains(string(body), "-- +goose Up") ||
			!strings.Contains(string(body), "-- +goose Down") {
			t.Fatalf("invalid embedded migration %s: %v", name, err)
		}
	}
}
