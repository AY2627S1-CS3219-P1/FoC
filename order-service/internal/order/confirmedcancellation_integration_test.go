//go:build integration

package order

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Q5/Q15: stale intent must fail before admission, while a retained successful
// intent must replay before examining the current revision or later amendments.
func TestPostgresConfirmedCancellationReplay(t *testing.T) {
	f := setup(t)
	courier := uuid.NewString()
	o := cancellationState(t, f, AwaitingPickup, courier)
	s := NewCanceller(f.repo, nil, AssignmentOptions{})
	f.mustTransition(t, o.ID, Pickup, Actor{Kind: User, ID: courier})
	key := uuid.NewString()
	if _, err := s.CancelConfirmed(f.ctx, Caller{ID: o.RequesterID}, o.ID, "stop", key, o.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale cancellation admitted: %v", err)
	}
	var retained int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_cancellations WHERE order_id=$1", o.ID).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("stale intent persisted: %d %v", retained, err)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "stop", key, current.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewDisposition(NewMutator(f.repo)).RecordItemLocation(f.ctx, Caller{ID: courier}, o.ID, "Residence reception"); err != nil {
		t.Fatal(err)
	}
	audit, facts, _ := f.counts(t, o.ID)
	replayed, err := s.CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "stop", key, current.Revision)
	if err != nil || replayed.OperationID != first.OperationID || replayed.Status != CancellationSucceeded || replayed.Order.ItemLocation == nil {
		t.Fatalf("retained replay=%+v err=%v", replayed, err)
	}
	if a, n, _ := f.counts(t, o.ID); a != audit || n != facts {
		t.Fatal("replay wrote lifecycle or settlement facts")
	}
	if _, err := s.CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "stop", key, current.Revision+1); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("changed confirmed revision reused key: %v", err)
	}
}

// Reproduced PR171 review finding: no configured Credit means no recoverer.
// Refuse new withdrawal without writing a pickup-blocking pending command.
func TestPostgresWithdrawalWithoutCreditRefusesNewIntent(t *testing.T) {
	f := setup(t)
	courier := uuid.NewString()
	o := cancellationState(t, f, AwaitingPickup, courier)
	audit, facts, _ := f.counts(t, o.ID)
	_, err := NewCanceller(f.repo, nil, AssignmentOptions{}).CancelConfirmed(f.ctx, Caller{ID: courier}, o.ID, "leave", uuid.NewString(), o.Revision)
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("new withdrawal without recoverer: %v", err)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.PendingCancellationID != nil || current.Revision != o.Revision {
		t.Fatalf("unrecoverable intent stranded order: %+v %v", current, err)
	}
	if a, n, _ := f.counts(t, o.ID); a != audit || n != facts {
		t.Fatal("unavailable withdrawal wrote")
	}
	// Requester terminal cancellation does not depend on Credit reachability.
	result, err := NewCanceller(f.repo, nil, AssignmentOptions{}).CancelConfirmed(f.ctx, Caller{ID: o.RequesterID}, o.ID, "stop", uuid.NewString(), o.Revision)
	if err != nil || result.Order.Status != Cancelled {
		t.Fatalf("requester cancellation unnecessarily blocked: %+v %v", result, err)
	}
	var holds bool
	if err := f.pool.QueryRow(f.ctx, "SELECT (details->>'courier_holds_item')::boolean FROM order_audit WHERE order_id=$1 AND action='cancel'", o.ID).Scan(&holds); err != nil || holds {
		t.Fatalf("missing explicit pre-pickup no-custody record: %v %v", holds, err)
	}
}
