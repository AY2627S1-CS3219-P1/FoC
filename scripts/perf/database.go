package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

var databaseEnvs = []string{"PERF_USER_DB", "PERF_SUPPLIER_DB", "PERF_ORDER_DB", "PERF_CREDIT_DB"}

func connectDB(ctx context.Context, env string) (*pgxpool.Pool, error) {
	url := os.Getenv(env)
	if url == "" {
		return nil, fmt.Errorf("%s is required (private database connection)", env)
	}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, fmt.Errorf("invalid %s configuration", env)
	}
	cfg.MaxConns = 8
	cfg.ConnConfig.RuntimeParams["application_name"] = "foc-perf"
	cfg.ConnConfig.ConnectTimeout = 10 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "10000"
	db, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("cannot configure %s", env)
	}
	if err := db.Ping(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("cannot connect using %s; check private routing and TLS", env)
	}
	return db, nil
}

func seedDatabases(ctx context.Context, m fixtures, directory string) error {
	if len(m.Files) != len(databaseEnvs) {
		return errors.New("expected four SQL fixture files")
	}
	// Connect to every target before the first write. Files are fixed names and
	// additive; there is no cleanup, TRUNCATE, schema reset or cross-DB atomicity.
	dbs := make([]*pgxpool.Pool, len(databaseEnvs))
	for i, env := range databaseEnvs {
		db, err := connectDB(ctx, env)
		if err != nil {
			return err
		}
		defer db.Close()
		dbs[i] = db
	}
	for i, name := range []string{"user.sql", "supplier.sql", "order.sql", "credit.sql"} {
		if m.Files[i] != name {
			return errors.New("unexpected SQL fixture filename")
		}
		data, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			return err
		}
		if _, err := dbs[i].Exec(ctx, string(data)); err != nil {
			return fmt.Errorf("%s failed; earlier service transactions may have committed: %w", name, err)
		}
	}
	return nil
}

func verifyDatabases(ctx context.Context, m fixtures, out string) error {
	details := make(map[string]any)
	passed := true
	checks := []struct {
		env, name, sql string
		ids            []string
		minimum        int
	}{
		{
			"PERF_USER_DB",
			"users",
			`SELECT count(*) FROM users WHERE id=ANY($1::uuid[]) AND deleted_at IS NULL AND role='user'`,
			m.UserIDs,
			10000,
		},
		{
			"PERF_SUPPLIER_DB",
			"suppliers",
			`SELECT count(*) FROM locations WHERE id=ANY($1::uuid[]) AND is_supplier AND archived_at IS NULL`,
			m.LocationIDs,
			1000,
		},
		{
			"PERF_ORDER_DB",
			"open",
			`SELECT count(*) FROM orders WHERE id=ANY($1::uuid[]) AND status='awaiting_courier'`,
			m.OpenIDs,
			10000,
		},
		{
			"PERF_ORDER_DB",
			"historical",
			`SELECT count(*) FROM orders WHERE id=ANY($1::uuid[]) AND status IN ('completed','cancelled','expired','failed')`,
			m.HistoryIDs,
			100000,
		},
	}
	for _, check := range checks {
		db, err := connectDB(ctx, check.env)
		if err != nil {
			return err
		}
		var count int
		err = db.QueryRow(ctx, check.sql, check.ids).Scan(&count)
		if err != nil {
			db.Close()
			return err
		}
		totalQueries := map[string]string{
			"users":      `SELECT count(*) FROM users WHERE deleted_at IS NULL`,
			"suppliers":  `SELECT count(*) FROM locations WHERE is_supplier AND archived_at IS NULL`,
			"open":       `SELECT count(*) FROM orders WHERE status='awaiting_courier'`,
			"historical": `SELECT count(*) FROM orders WHERE status IN ('completed','cancelled','expired','failed')`,
		}
		var total int
		err = db.QueryRow(ctx, totalQueries[check.name]).Scan(&total)
		db.Close()
		if err != nil {
			return err
		}
		details[check.name] = count
		details[check.name+"Total"] = total
		passed = passed && count == len(check.ids) && count >= check.minimum
	}
	db, err := connectDB(ctx, "PERF_CREDIT_DB")
	if err != nil {
		return err
	}
	defer db.Close()
	var inconsistent, accounts, missingReservations int
	err = db.QueryRow(ctx, financialConsistencySQL, m.UserIDs).Scan(&inconsistent, &accounts)
	if err != nil {
		return err
	}
	err = db.QueryRow(ctx, `SELECT cardinality($1::uuid[])-count(*) FROM reservations WHERE order_id=ANY($1::uuid[])`, append(append([]string{}, m.OpenIDs...), m.HistoryIDs...)).
		Scan(&missingReservations)
	if err != nil {
		return err
	}
	details["inconsistentAccounts"] = inconsistent
	details["accounts"] = accounts
	details["missingReservations"] = missingReservations
	details["seed"] = m.Spec.Seed
	details["fixtures"] = m.Spec
	passed = passed && inconsistent == 0 && accounts == m.Spec.Users && missingReservations == 0
	return recordResult(out, "dataset", details, passed)
}

const financialConsistencySQL = `
WITH ledger AS (
 SELECT user_id,sum(balance_delta) balance,sum(reserved_delta) reserved
 FROM ledger_entries WHERE user_id=ANY($1::uuid[]) GROUP BY user_id
), holds AS (
 SELECT requester_id,sum(reserved_amount) reserved FROM reservations
 WHERE requester_id=ANY($1::uuid[]) AND status IN ('HELD','ASSIGNED') GROUP BY requester_id
)
SELECT count(*) FILTER (WHERE a.balance<>coalesce(l.balance,0) OR a.reserved<>coalesce(l.reserved,0)
 OR a.reserved<>coalesce(h.reserved,0) OR a.balance<a.reserved), count(*)
FROM accounts a LEFT JOIN ledger l ON l.user_id=a.user_id LEFT JOIN holds h ON h.requester_id=a.user_id
WHERE a.user_id=ANY($1::uuid[])`

func waitFor(ctx context.Context, timeout, poll time.Duration, check func() (bool, error)) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-deadline.C:
			timer.Stop()
			return errors.New("committed state was not observed before deadline")
		case <-timer.C:
		}
	}
}
