package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPostgreSQLProvisioningRetryOwnershipAndPrivilegeCleanup(t *testing.T) {
	socket := os.Getenv("AWS_SQL_TEST_SOCKET")
	if socket == "" {
		t.Skip("optional disposable PostgreSQL 16+ Unix socket not configured")
	}
	if !filepath.IsAbs(socket) {
		t.Fatal("SQL fixture requires an explicit local Unix socket")
	}
	client := os.Getenv("PSQL")
	if client == "" {
		client = "psql"
	}
	port := os.Getenv("AWS_SQL_TEST_PORT")
	if port == "" {
		port = "55439"
	}
	sqlIn := func(query, user, database string) (string, error) {
		cmd := exec.Command(
			client,
			"-X",
			"-h",
			socket,
			"-p",
			port,
			"-U",
			user,
			"-d",
			database,
			"--set",
			"ON_ERROR_STOP=1",
			"-At",
		)
		cmd.Stdin = strings.NewReader(query)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	sql := func(query, user string) (string, error) { return sqlIn(query, user, "postgres") }
	if output, err := sql("CREATE ROLE foc_admin LOGIN CREATEROLE CREATEDB;", "fixture_owner"); err != nil {
		t.Fatalf("fixture role: %s", output)
	}
	// Content moderation's original database, owned by the bare moderation login, holds data to keep.
	if output, err := sql(
		"CREATE ROLE foc_moderation LOGIN PASSWORD 'legacy-test-only';"+
			"GRANT foc_moderation TO foc_admin WITH INHERIT TRUE, SET TRUE;"+
			"CREATE DATABASE moderation_db OWNER foc_moderation;"+
			"GRANT foc_moderation TO foc_admin WITH INHERIT FALSE, SET FALSE;",
		"foc_admin",
	); err != nil {
		t.Fatalf("legacy content moderation fixture: %s", output)
	}
	if output, err := sqlIn(
		"CREATE TABLE legacy_jobs (state text); INSERT INTO legacy_jobs VALUES ('pending');",
		"foc_moderation",
		"moderation_db",
	); err != nil {
		t.Fatalf("legacy content moderation data: %s", output)
	}
	a, directory, _ := testApplication(t)
	config := testConfiguration()
	config["moderation_enabled"] = true
	legacy, err := json.Marshal(map[string]any{
		"DB_HOST":      stringValue(testConfiguration(), "database_host"),
		"DB_USER":      "foc_moderation",
		"DB_PASSWORD":  "legacy-test-only",
		"DB_NAME":      "moderation_db",
		"DATABASE_URL": "postgresql://unused",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "moderation.json"), legacy, 0600); err != nil {
		t.Fatal(err)
	}
	session := exec.Command(client, "-X", "-h", socket, "-p", port, "-U", "foc_moderation", "-d", "moderation_db",
		"-c", "SELECT pg_sleep(60)")
	if err := session.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Process.Kill(); _ = session.Wait() }()
	for attempt := 0; ; attempt++ {
		output, err := sql("SELECT count(*) FROM pg_stat_activity WHERE datname = 'moderation_db';", "fixture_owner")
		if err == nil && strings.TrimSpace(output) == "1" {
			break
		}
		if attempt == 50 {
			t.Fatal("legacy content moderation session did not connect")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := a.prepareDatabase(
		config,
		map[string]any{"username": "foc_admin", "password": "offline-test-only"},
		directory,
	); err != nil {
		t.Fatal(err)
	}
	secrets := readTestObject(t, filepath.Join(directory, "secrets.json"))
	setup := ""
	for _, item := range array(secrets["items"]) {
		value := object(item)
		if stringValue(object(value["metadata"]), "name") == "rds-provision-admin" {
			setup = stringValue(object(value["stringData"]), "setup.sql")
		}
	}
	if setup == "" {
		t.Fatal("provisioning SQL missing")
	}
	// A local CREATEROLE fixture is not RDS's privileged extension administrator.
	lines := []string{}
	for _, line := range strings.Split(setup, "\n") {
		if !strings.HasPrefix(line, "CREATE EXTENSION") {
			lines = append(lines, line)
		}
	}
	setup = strings.Join(lines, "\n")
	for _, attempt := range []string{"initial provisioning", "idempotent retry"} {
		if output, err := sql(setup, "foc_admin"); err != nil {
			t.Fatalf("%s: %s", attempt, output)
		}
	}
	output, err := sqlIn("SELECT state FROM legacy_jobs;", "foc_content_moderation", "content_moderation_db")
	if err != nil || strings.TrimSpace(output) != "pending" {
		t.Fatalf("content moderation data did not follow the renamed database: %s", output)
	}
	output, err = sqlIn(
		"SELECT count(*) FROM pg_tables WHERE tablename = 'legacy_jobs';",
		"foc_moderation",
		"moderation_db",
	)
	if err != nil || strings.TrimSpace(output) != "0" {
		t.Fatalf("reports/appeals inherited content moderation's original database: %s", output)
	}
	output, err = sql(
		"SELECT datname || ':' || pg_get_userbyid(datdba) FROM pg_database WHERE datname IN ('user_db','supplier_db','order_db','credit_db','communication_db','content_moderation_db','moderation_db');",
		"fixture_owner",
	)
	if err != nil {
		t.Fatal(output)
	}
	for _, expected := range []string{"user_db:foc_user", "supplier_db:foc_supplier", "order_db:foc_order", "credit_db:foc_credit", "communication_db:foc_communication", "content_moderation_db:foc_content_moderation", "moderation_db:foc_moderation"} {
		if !strings.Contains(output, expected) {
			t.Fatal("service database has wrong owner")
		}
	}
	output, err = sql(
		"SELECT count(*) FROM pg_auth_members WHERE member = 'foc_admin'::regrole AND (set_option OR inherit_option);",
		"fixture_owner",
	)
	if err != nil || strings.TrimSpace(output) != "0" {
		t.Fatal("temporary SET/INHERIT privileges remain")
	}
	// Each service owns its database, not a shared schema or another service's data.
	names := []string{"user", "supplier", "order", "credit", "communication", "content_moderation", "moderation"}
	for _, service := range names {
		for _, database := range names {
			cmd := exec.Command(client, "-X", "-h", socket, "-p", port, "-U", "foc_"+service,
				"-d", database+"_db", "-Atc", "SELECT current_user")
			output, err := cmd.CombinedOutput()
			if service == database {
				if err != nil || strings.TrimSpace(string(output)) != "foc_"+service {
					t.Fatalf("%s cannot access its own database: %s", service, output)
				}
			} else if err == nil || !strings.Contains(string(output), "permission denied for database") {
				t.Fatalf("%s must not access %s's database: %s", service, database, output)
			}
		}
	}
	if output, err := sql("ALTER DATABASE order_db OWNER TO fixture_owner;", "fixture_owner"); err != nil {
		t.Fatal(output)
	}
	output, err = sql(setup, "foc_admin")
	if err == nil || !strings.Contains(output, "Existing database owner does not match") {
		t.Fatal("wrong database owner did not stop provisioning with a nonzero exit")
	}
	if output, err := sql("ALTER DATABASE order_db OWNER TO foc_order;", "fixture_owner"); err != nil {
		t.Fatal(output)
	}
	if output, err := sql(setup, "foc_admin"); err != nil {
		t.Fatal(output)
	}
	t.Log(
		"PASS: real PostgreSQL created seven owned databases, renamed content moderation's original database in place, gave reports/appeals an empty database, allowed seven owner connections, denied all 42 cross-service connections, retried, rejected wrong ownership, and removed temporary privileges",
	)
}
