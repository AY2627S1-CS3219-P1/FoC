package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"github.com/jackc/pgx/v5"
)

type databaseAssertion struct {
	Name        string `json:"name"`
	DatabaseEnv string `json:"databaseEnv"`
	SQL         string `json:"sql"`
	Args        []any  `json:"args"`
	Expected    any    `json:"expected"`
}

func assertDatabases(ctx context.Context, path, out string) error {
	var checks []databaseAssertion
	if err := readJSON(path, &checks); err != nil {
		return err
	}
	if len(checks) == 0 {
		return errors.New("at least one concrete assertion is required")
	}
	evidence := []map[string]any{}
	passed := true
	for _, check := range checks {
		if check.Name == "" || check.SQL == "" {
			return errors.New("assertions require a name and query")
		}
		db, err := connectDB(ctx, check.DatabaseEnv)
		if err != nil {
			return err
		}
		tx, err := db.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if err != nil {
			db.Close()
			return err
		}
		var actual any
		err = tx.QueryRow(ctx, check.SQL, check.Args...).Scan(&actual)
		_ = tx.Rollback(ctx)
		db.Close()
		if err != nil {
			return fmt.Errorf("assertion %s failed: %w", check.Name, err)
		}
		// Normalize DB integers/JSON and JSON-decoded expected values to the
		// same representation before comparing; do not accept a truthy string.
		data, err := json.Marshal(actual)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(data, &actual); err != nil {
			return err
		}
		ok := reflect.DeepEqual(actual, check.Expected)
		passed = passed && ok
		evidence = append(
			evidence,
			map[string]any{
				"name":     check.Name,
				"query":    check.SQL,
				"args":     check.Args,
				"actual":   actual,
				"expected": check.Expected,
				"passed":   ok,
			},
		)
	}
	status := "pass"
	if !passed {
		status = "fail"
	}
	output := map[string]any{"status": status, "checks": evidence}
	if err := saveJSON(filepath.Join(out, "assertions.json"), output); err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		return err
	}
	if !passed {
		return errors.New("database assertions failed")
	}
	return nil
}
