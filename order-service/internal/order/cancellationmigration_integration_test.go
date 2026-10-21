//go:build integration

package order

import (
	"database/sql"
	"os"
	"strings"
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
	if _, err := provider.DownTo(f.ctx, 5); err != nil {
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

func TestPostgresCancellationMigrationRefusesPendingWithdrawal(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	credit := newAssignmentCredit(100, o)
	courier := Caller{ID: uuid.NewString()}
	assigned, err := NewAssigner(f.repo, credit, AssignmentOptions{Now: now}).AcceptOrder(f.ctx, courier, o.ID, uuid.NewString())
	if err != nil || assigned.Phase != AssignmentSucceeded {
		t.Fatalf("assign: %+v %v", assigned, err)
	}
	credit.denyRevert = true
	key := uuid.NewString()
	pending, err := NewCanceller(f.repo, credit, AssignmentOptions{Now: now}).Cancel(f.ctx, courier, o.ID, "withdraw", key)
	if err != nil || pending.Status != CancellationPending {
		t.Fatalf("withdraw: %+v %v", pending, err)
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
	if _, err := provider.DownTo(f.ctx, 5); err == nil || !strings.Contains(err.Error(), "cannot downgrade while courier withdrawals are pending") {
		t.Fatalf("pending downgrade: %v", err)
	}
	var assignmentID, resourceID string
	if err := f.pool.QueryRow(f.ctx, "SELECT assignment_id FROM order_cancellations WHERE id=$1 AND status='pending'", pending.OperationID).Scan(&assignmentID); err != nil || assignmentID != assigned.AttemptID {
		t.Fatalf("downgrade lost reversal identity: %s %v", assignmentID, err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT resource_id FROM order_idempotency_keys WHERE idempotency_key=$1", key).Scan(&resourceID); err != nil || resourceID != pending.OperationID {
		t.Fatalf("downgrade lost retry identity: %s %v", resourceID, err)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.PendingCancellationID == nil || *current.PendingCancellationID != pending.OperationID {
		t.Fatalf("downgrade lost pending reference: %+v %v", current, err)
	}
	closed, err := NewCanceller(f.repo, nil, AssignmentOptions{Now: now}).Cancel(f.ctx, Caller{ID: o.RequesterID}, o.ID, "cancel", "")
	if err != nil || closed.Status != CancellationSucceeded {
		t.Fatalf("supersede: %+v %v", closed, err)
	}
	if _, err := provider.DownTo(f.ctx, 5); err != nil {
		t.Fatalf("downgrade after supersession: %v", err)
	}
	if _, err := provider.Up(f.ctx); err != nil {
		t.Fatalf("upgrade after downgrade: %v", err)
	}
}
