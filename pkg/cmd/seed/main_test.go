package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeManifest(t *testing.T, directory string, files []fixture) string {
	t.Helper()
	contents, err := json.Marshal(manifest{Version: 1, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "manifest.json")
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func writeSQL(t *testing.T, directory, name, sql string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(sql), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRunUsesManifestOrderTargetsContentsAndReset(t *testing.T) {
	directory := t.TempDir()
	// Deliberately reverse alphabetical order to catch implicit sorting.
	files := []fixture{{File: "z.sql", Database: "USER_DB"}, {File: "a.sql", Database: "SUPPLIER_DB"}}
	writeSQL(t, directory, "z.sql", "BEGIN; SELECT 'first'; COMMIT;")
	writeSQL(t, directory, "a.sql", "BEGIN; SELECT 'second'; COMMIT;")
	path := writeManifest(t, directory, files)
	for _, reset := range []bool{false, true} {
		var got []string
		err := run(context.Background(), path, reset, func(_ context.Context, f fixture, actualReset bool) error {
			if actualReset != reset {
				t.Fatal("reset flag changed")
			}
			if !strings.HasPrefix(string(f.content), "BEGIN;") || !strings.HasSuffix(string(f.content), "COMMIT;") {
				t.Fatal("runner changed SQL transaction boundaries")
			}
			got = append(got, f.File+":"+f.Database)
			return nil
		}, io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, []string{"z.sql:USER_DB", "a.sql:SUPPLIER_DB"}) {
			t.Fatalf("execution order/targets: %v", got)
		}
	}
}

func TestRunStopsOnFirstFailure(t *testing.T) {
	directory := t.TempDir()
	var files []fixture
	for _, name := range []string{"one.sql", "two.sql", "three.sql"} {
		writeSQL(t, directory, name, "SELECT 1;")
		files = append(files, fixture{File: name, Database: "USER_DB"})
	}
	path := writeManifest(t, directory, files)
	for _, failAt := range []int{1, 2} {
		calls := 0
		failure := errors.New("psql failed")
		err := run(context.Background(), path, false, func(_ context.Context, f fixture, _ bool) error {
			calls++
			if calls == failAt {
				return failure
			}
			return nil
		}, io.Discard)
		if !errors.Is(err, failure) || calls != failAt || !strings.Contains(err.Error(), files[failAt-1].File) {
			t.Fatalf("calls=%d error=%v", calls, err)
		}
	}
}

func TestInvalidManifestCannotExecuteAnyFile(t *testing.T) {
	for name, body := range map[string]string{
		"version":             `{"version":2,"files":[{"file":"ok.sql","database":"USER_DB"}]}`,
		"empty":               `{"version":1,"files":[]}`,
		"unknown field":       `{"version":1,"files":[],"other":true}`,
		"unknown entry field": `{"version":1,"files":[{"file":"ok.sql","database":"USER_DB","other":true}]}`,
		"trailing object":     `{"version":1,"files":[]} {}`,
		"bad target":          `{"version":1,"files":[{"file":"ok.sql","database":"USER_DB; echo bad"}]}`,
		"traversal":           `{"version":1,"files":[{"file":"../outside.sql","database":"USER_DB"}]}`,
		"missing later file":  `{"version":1,"files":[{"file":"ok.sql","database":"USER_DB"},{"file":"missing.sql","database":"SUPPLIER_DB"}]}`,
		"duplicate":           `{"version":1,"files":[{"file":"ok.sql","database":"USER_DB"},{"file":"./ok.sql","database":"USER_DB"}]}`,
		"not SQL":             `{"version":1,"files":[{"file":"manifest.json","database":"USER_DB"}]}`,
		"empty SQL":           `{"version":1,"files":[{"file":"empty.sql","database":"USER_DB"}]}`,
		"symlink":             `{"version":1,"files":[{"file":"link.sql","database":"USER_DB"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeSQL(t, directory, "ok.sql", "SELECT 1;")
			writeSQL(t, directory, "empty.sql", " \n")
			outside := filepath.Join(t.TempDir(), "outside.sql")
			if err := os.WriteFile(outside, []byte("SELECT 1;"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, filepath.Join(directory, "link.sql")); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "manifest.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			called := false
			err := run(context.Background(), path, false, func(context.Context, fixture, bool) error { called = true; return nil }, io.Discard)
			if err == nil || called {
				t.Fatalf("invalid manifest executed=%v error=%v", called, err)
			}
		})
	}
}
