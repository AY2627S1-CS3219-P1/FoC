//go:build integration

package order

import (
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
)

func TestPostgresCancellationMigrationWithData(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	key := uuid.NewString()
	result, err := NewCanceller(f.repo, nil, AssignmentOptions{Now: now}).Cancel(f.ctx, Caller{ID: o.RequesterID}, o.ID, "migration", key)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", f.pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(f.ctx); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := f.pool.QueryRow(f.ctx, "SELECT status FROM orders WHERE id=$1", o.ID).Scan(&status); err != nil || Status(status) != Cancelled {
		t.Fatalf("rollback damaged order %s %v", status, err)
	}
	if _, err := provider.Up(f.ctx); err != nil {
		t.Fatal(err)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.Revision != result.Order.Revision || current.Status != Cancelled || current.CloseReason == nil || *current.CloseReason != "migration" {
		t.Fatalf("upgrade damaged order %+v %v", current, err)
	}
	var keys int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_idempotency_keys WHERE idempotency_key=$1", key).Scan(&keys); err != nil || keys != 0 {
		t.Fatalf("rollback left dangling key %d %v", keys, err)
	}
}
