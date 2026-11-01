//go:build integration

// Package postgres starts a throwaway PostgreSQL with Credit's migrations.
package postgres

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func Setup(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:18",
		postgres.WithDatabase("credit_test"),
		postgres.WithUsername("credit"),
		postgres.WithPassword("credit"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start PostgreSQL: %v", err)
	}
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer db.Close() //nolint:errcheck
	_, file, _, _ := runtime.Caller(0)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, filepath.Join(filepath.Dir(file), "..", "..", "..", "database", "schema")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// CheckLedger fails the test unless every account equals the sum of its
// ledger, reserved credits equal the requester's active holds, and available
// credits are not negative.
func CheckLedger(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT a.user_id::text, a.balance, a.reserved,
			COALESCE((SELECT sum(balance_delta) FROM ledger_entries l WHERE l.user_id = a.user_id), 0)::bigint,
			COALESCE((SELECT sum(reserved_delta) FROM ledger_entries l WHERE l.user_id = a.user_id), 0)::bigint,
			COALESCE((SELECT sum(reserved_amount) FROM reservations r
				WHERE r.requester_id = a.user_id AND r.status IN ('HELD', 'ASSIGNED')), 0)::bigint
		FROM accounts a`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var balance, reserved, ledgerBalance, ledgerReserved, held int64
		if err := rows.Scan(&id, &balance, &reserved, &ledgerBalance, &ledgerReserved, &held); err != nil {
			t.Fatal(err)
		}
		if balance != ledgerBalance || reserved != ledgerReserved || reserved != held || balance < reserved {
			t.Fatalf(
				"account %s: balance %d reserved %d; ledger %d/%d; holds %d",
				id,
				balance,
				reserved,
				ledgerBalance,
				ledgerReserved,
				held,
			)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
