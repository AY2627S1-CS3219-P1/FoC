package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

var services = []string{"user", "supplier", "order", "credit", "communication"}

func (a *application) prepareDatabase(config, admin map[string]any, directory string) error {
	directory, err := privatePath(a.root, directory)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return errors.New("cannot create private database directory")
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return errors.New("cannot protect private database directory")
	}
	host := stringValue(config, "database_host")
	if host == "" || stringValue(admin, "username") == "" || stringValue(admin, "password") == "" {
		return errors.New("database preparation requires a host and private administrator credentials")
	}
	if value, ok := admin["host"]; ok && value != host {
		return errors.New("administrator secret belongs to a different database host")
	}
	databaseServices := append([]string(nil), services...)
	if enabled, exists := config["moderation_enabled"]; exists {
		value, ok := enabled.(bool)
		if !ok {
			return errors.New("moderation_enabled must be a boolean")
		}
		if value {
			databaseServices = append(databaseServices, "moderation")
		}
	}
	objects := []any{}
	statements := []string{`\set ON_ERROR_STOP on`}
	cleanup := []string{}
	for _, service := range databaseServices {
		path := filepath.Join(directory, service+".json")
		var value map[string]any
		if _, err := os.Stat(path); err == nil {
			value, err = readObject(path)
			if err != nil {
				return err
			}
			if stringValue(value, "DB_HOST") != host {
				return fmt.Errorf("retained %s credentials belong to a different RDS host", service)
			}
		} else if !os.IsNotExist(err) {
			return errors.New("cannot inspect retained database credentials")
		} else {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return errors.New("cannot generate database credentials")
			}
			password := base64.RawURLEncoding.EncodeToString(raw)
			username := "foc_" + service
			database := service + "_db"
			query := url.Values{"sslmode": {"verify-full"}, "sslrootcert": {"/var/run/foc/rds/global-bundle.pem"}}
			connection := url.URL{
				Scheme:   "postgresql",
				User:     url.UserPassword(username, password),
				Host:     host + ":5432",
				Path:     "/" + database,
				RawQuery: query.Encode(),
			}
			value = map[string]any{
				"DB_HOST":      host,
				"DB_USER":      username,
				"DB_PASSWORD":  password,
				"DB_NAME":      database,
				"DATABASE_URL": connection.String(),
			}
			if err := saveJSON(a.root, path, value); err != nil {
				return err
			}
		}
		if err := os.Chmod(path, 0600); err != nil {
			return errors.New("cannot protect database credentials")
		}
		objects = append(
			objects,
			map[string]any{
				"apiVersion": "v1",
				"kind":       "Secret",
				"type":       "Opaque",
				"metadata":   map[string]any{"name": service + "-db", "namespace": "foc"},
				"stringData": value,
			},
		)
		for _, key := range []string{"DB_USER", "DB_PASSWORD", "DB_NAME"} {
			text, ok := value[key].(string)
			if !ok || text == "" {
				return fmt.Errorf("retained %s credentials are incomplete", service)
			}
			escaped := strings.ReplaceAll(strings.ReplaceAll(text, "'", "''"), `\`, `\\`)
			statements = append(statements, fmt.Sprintf(`\set %s '%s'`, strings.ToLower(key), escaped))
		}
		statements = append(
			statements,
			`SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'db_user', :'db_password') WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'db_user') \gexec`,
			`SELECT format('GRANT %I TO %I WITH INHERIT TRUE, SET TRUE', :'db_user', current_user) \gexec`,
			`SELECT format('CREATE DATABASE %I OWNER %I', :'db_name', :'db_user') WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name') \gexec`,
			`SELECT pg_get_userbyid(datdba) = :'db_user' AS owner_matches FROM pg_database WHERE datname = :'db_name' \gset`,
			`\if :owner_matches`,
			`\else`,
			`DO $$ BEGIN RAISE EXCEPTION 'Existing database owner does not match the expected service role. Aborting.'; END $$;`,
			`\endif`,
			`SELECT format('REVOKE CONNECT ON DATABASE %I FROM PUBLIC', :'db_name') \gexec`,
			`SELECT format('GRANT CONNECT ON DATABASE %I TO %I', :'db_name', :'db_user') \gexec`,
		)
		username := strings.ReplaceAll(stringValue(value, "DB_USER"), "'", "''")
		cleanup = append(
			cleanup,
			fmt.Sprintf(
				`SELECT format('GRANT %%I TO %%I WITH INHERIT FALSE, SET FALSE', '%s', current_user) \gexec`,
				username,
			),
		)
	}
	statements = append(
		statements,
		`\connect supplier_db`,
		`CREATE EXTENSION IF NOT EXISTS postgis;`,
		`CREATE EXTENSION IF NOT EXISTS pg_trgm;`,
		`\connect postgres`,
	)
	statements = append(statements, cleanup...)
	objects = append(
		objects,
		map[string]any{
			"apiVersion": "v1",
			"kind":       "Secret",
			"type":       "Opaque",
			"metadata":   map[string]any{"name": "rds-provision-admin", "namespace": "foc"},
			"stringData": map[string]any{
				"PGHOST":        host,
				"PGPORT":        "5432",
				"PGDATABASE":    "postgres",
				"PGUSER":        admin["username"],
				"PGPASSWORD":    admin["password"],
				"PGSSLMODE":     "verify-full",
				"PGSSLROOTCERT": "/var/run/foc/rds/global-bundle.pem",
				"setup.sql":     strings.Join(statements, "\n") + "\n",
			},
		},
		map[string]any{
			"apiVersion":                   "v1",
			"kind":                         "ServiceAccount",
			"metadata":                     map[string]any{"name": "rds-provision", "namespace": "foc"},
			"automountServiceAccountToken": false,
		},
	)
	if err := saveJSON(
		a.root,
		filepath.Join(directory, "secrets.json"),
		map[string]any{"apiVersion": "v1", "kind": "List", "items": objects},
	); err != nil {
		return err
	}
	if err := saveJSON(
		a.root,
		filepath.Join(directory, "job.json"),
		json.RawMessage(databaseProvisionJob),
	); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(
		a.out,
		"Prepared %d service database Secrets and the RDS provisioning Job in %s\n",
		len(databaseServices),
		directory,
	)
	_, _ = fmt.Fprintln(
		a.out,
		"No resources applied. Do not print or commit secrets.json or the per-service credential files.",
	)
	return nil
}
