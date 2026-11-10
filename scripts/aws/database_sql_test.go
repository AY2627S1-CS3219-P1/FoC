package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
	sql := func(query, user string) (string, error) {
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
			"postgres",
			"--set",
			"ON_ERROR_STOP=1",
			"-At",
		)
		cmd.Stdin = strings.NewReader(query)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := sql("CREATE ROLE foc_admin LOGIN CREATEROLE CREATEDB;", "fixture_owner"); err != nil {
		t.Fatalf("fixture role: %s", output)
	}
	a, directory, _ := testApplication(t)
	config := testConfiguration()
	config["moderation_enabled"] = true
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
	output, err := sql(
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
		"PASS: real PostgreSQL created seven owned databases, allowed seven owner connections, denied all 42 cross-service connections, retried, rejected wrong ownership, and removed temporary privileges",
	)
}
