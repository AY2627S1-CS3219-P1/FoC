//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This test needs Docker only: PostgreSQL and psql run inside a disposable
// container. It never connects to the development databases.
func TestRunnerPSQLRepeatabilityResetAndFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	start := exec.CommandContext(ctx, "docker", "run", "--rm", "-d",
		"-e", "POSTGRES_USER=foc", "-e", "POSTGRES_PASSWORD=fixture-test-only",
		"-e", "USER_DB=user_seed_test", "-e", "SUPPLIER_DB=supplier_seed_test", "postgis/postgis:18-3.6")
	var stdout, stderr bytes.Buffer
	start.Stdout, start.Stderr = &stdout, &stderr
	if err := start.Run(); err != nil {
		t.Fatalf("start PostgreSQL: %v: %s", err, stderr.String())
	}
	// With a cold image cache, docker run streams the pull progress to stderr
	// and prints the container ID as the last stdout line.
	container := strings.TrimSpace(stdout.String())
	if line := strings.LastIndexByte(container, '\n'); line >= 0 {
		container = strings.TrimSpace(container[line+1:])
	}
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		if output, err := exec.CommandContext(cleanupCtx, "docker", "rm", "-f", container).
			CombinedOutput(); err != nil {
			t.Errorf("remove test container: %v: %s", err, output)
		}
	})
	psql := func(database, sql string) (string, error) {
		command := exec.CommandContext(
			ctx,
			"docker",
			"exec",
			"-i",
			container,
			"psql",
			"-h",
			"127.0.0.1",
			"-X",
			"-qAt",
			"-U",
			"foc",
			"-d",
			database,
			"-v",
			"ON_ERROR_STOP=1",
		)
		command.Stdin = strings.NewReader(sql)
		output, err := command.CombinedOutput()
		return strings.TrimSpace(string(output)), err
	}
	// The image initializes through a temporary socket-only server, then
	// restarts. TCP readiness selects the final server and avoids that restart.
	// A cold runner also has to pull the image first, so allow the same
	// three-minute budget the rest of the integration suite uses; slow pulls
	// degrade to a slower pass instead of a failure.
	deadline := time.Now().Add(3 * time.Minute)
	for {
		if _, err := psql("postgres", "SELECT 1"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			// The poll loop discards psql errors while PostgreSQL is starting,
			// so report the container state and logs on timeout.
			inspect := exec.Command("docker", "inspect", "-f",
				"{{.State.Status}} exit={{.State.ExitCode}}", container)
			state, inspectErr := inspect.CombinedOutput()
			logs, logsErr := exec.Command("docker", "logs", "--tail", "100", container).CombinedOutput()
			t.Fatalf("PostgreSQL did not become ready: state %v: %s; logs %v:\n%s",
				inspectErr, state, logsErr, logs)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
	mustSQL := func(database, sql string) string {
		t.Helper()
		output, err := psql(database, sql)
		if err != nil {
			t.Fatalf("psql %s: %v: %s", database, err, output)
		}
		return output
	}
	mustSQL("postgres", "CREATE DATABASE user_seed_test;")
	mustSQL("postgres", "CREATE DATABASE supplier_seed_test;")
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []struct{ database, path string }{{"user_seed_test", "user-service/migrations"}, {"supplier_seed_test", "supplier-service/database/schema"}} {
		paths, err := filepath.Glob(filepath.Join(root, target.path, "*.sql"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("migration files: %v", err)
		}
		for _, path := range paths {
			contents, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// Goose's Up/Down boundary is respected; Goose directives inside
			// Up are SQL comments and psql accepts the existing function bodies.
			mustSQL(target.database, strings.Split(string(contents), "-- +goose Down")[0])
		}
	}
	var seedStderr bytes.Buffer
	execute := psqlExecutor([]string{"docker", "exec", "-i", container}, io.Discard, &seedStderr)
	path := filepath.Join(root, "data", "seeds", "manifest.json")
	seed := func(reset bool) {
		t.Helper()
		seedStderr.Reset()
		if err := run(ctx, path, reset, execute, io.Discard); err != nil {
			t.Fatalf("seed: %v: %s", err, seedStderr.String())
		}
	}
	// Existing CSV reference records must be reused without changing their data.
	mustSQL(
		"supplier_seed_test",
		"INSERT INTO buildings(id,name,center,radius_m) VALUES ('10000000-0000-4000-8000-000000000001','Hon Sui Sen Memorial Library',ST_SetSRID(ST_MakePoint(103.77,1.29),4326)::geography,100)",
	)
	mustSQL(
		"supplier_seed_test",
		"INSERT INTO categories(id,name) VALUES ('10000000-0000-4000-8000-000000000002','Food')",
	)
	seed(false)
	if got := mustSQL(
		"supplier_seed_test",
		"SELECT building_id FROM locations WHERE id='f0c00004-0000-4000-8000-000000000001'",
	); got != "10000000-0000-4000-8000-000000000001" {
		t.Fatalf("existing building was not reused: %s", got)
	}
	if got := mustSQL(
		"supplier_seed_test",
		"SELECT count(*) FROM location_categories WHERE location_id='f0c00004-0000-4000-8000-000000000001' AND category_id='10000000-0000-4000-8000-000000000002'",
	); got != "1" {
		t.Fatal("existing category was not reused")
	}
	if got := mustSQL("user_seed_test", "SELECT count(*) FROM users"); got != "6" {
		t.Fatalf("users=%s", got)
	}
	if got := mustSQL("supplier_seed_test", "SELECT count(*) FROM locations"); got != "34" {
		t.Fatalf("locations=%s", got)
	}
	mustSQL("user_seed_test", "UPDATE users SET description='manual edit' WHERE email='admin@demo.foc.test'")
	mustSQL(
		"supplier_seed_test",
		"UPDATE locations SET name='manual edit',revision=revision+1 WHERE id='f0c00004-0000-4000-8000-000000000001'",
	)
	revision := mustSQL(
		"supplier_seed_test",
		"SELECT revision FROM locations WHERE id='f0c00004-0000-4000-8000-000000000001'",
	)
	seed(false)
	if got := mustSQL(
		"user_seed_test",
		"SELECT description FROM users WHERE email='admin@demo.foc.test'",
	); got != "manual edit" {
		t.Fatalf("normal seed changed profile: %q", got)
	}
	if got := mustSQL(
		"supplier_seed_test",
		"SELECT name || ':' || revision FROM locations WHERE id='f0c00004-0000-4000-8000-000000000001'",
	); got != "manual edit:"+revision {
		t.Fatalf("normal seed changed location: %q", got)
	}
	seed(true)
	if got := mustSQL(
		"supplier_seed_test",
		"SELECT radius_m FROM buildings WHERE id='10000000-0000-4000-8000-000000000001'",
	); got != "100" {
		t.Fatalf("reset changed the shared building: %s", got)
	}
	if got := mustSQL(
		"user_seed_test",
		"SELECT count(*) FROM users WHERE email='admin@demo.foc.test' AND description='manual edit'",
	); got != "0" {
		t.Fatal("reset did not restore user")
	}
	if got := mustSQL(
		"supplier_seed_test",
		fmt.Sprintf(
			"SELECT count(*) FROM locations WHERE id='f0c00004-0000-4000-8000-000000000001' AND name='TOMORO COFFEE - HSSML' AND revision>%s",
			revision,
		),
	); got != "1" {
		t.Fatal("reset did not restore location/increase revision")
	}
	if got := mustSQL(
		"supplier_seed_test",
		"SELECT count(*) FROM location_addition_requests WHERE status='pending'",
	); got != "24" {
		t.Fatalf("pending requests=%s", got)
	}
	if got := mustSQL("supplier_seed_test", "SELECT count(*) FROM location_disablements"); got != "32" {
		t.Fatalf("closures=%s", got)
	}

	// Successful earlier files remain committed; the failing file rolls back
	// its own transaction and later files must never execute.
	directory := t.TempDir()
	writeSQL(
		t,
		directory,
		"z-first.sql",
		"BEGIN; CREATE TABLE runner_order(marker text PRIMARY KEY); INSERT INTO runner_order VALUES ('first'); COMMIT;",
	)
	writeSQL(t, directory, "a-fail.sql", "BEGIN; INSERT INTO runner_order VALUES ('failed'); SELECT 1/0; COMMIT;")
	writeSQL(t, directory, "m-last.sql", "BEGIN; INSERT INTO runner_order VALUES ('last'); COMMIT;")
	badPath := writeManifest(
		t,
		directory,
		[]fixture{
			{File: "z-first.sql", Database: "USER_DB"},
			{File: "a-fail.sql", Database: "USER_DB"},
			{File: "m-last.sql", Database: "USER_DB"},
		},
	)
	err = run(ctx, badPath, false, execute, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "a-fail.sql") {
		t.Fatalf("expected second-file failure: %v", err)
	}
	if got := mustSQL(
		"user_seed_test",
		"SELECT string_agg(marker,',' ORDER BY marker) FROM runner_order",
	); got != "first" {
		t.Fatalf("transaction rollback/failure handling: %s", got)
	}
}
