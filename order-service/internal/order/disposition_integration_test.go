//go:build integration

package order

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Q2-Q5/Q8: exercise real transaction/audit/outbox boundaries, stale pickup
// confirmation, concurrent disposition retries, corrections and audit rollback.
// Uses the existing disposable PostgreSQL 18 fixture, never shared service data.
func TestPostgresCancellationDisposition(t *testing.T) {
	f := setup(t)
	f.reset(t)
	o := f.create(t)
	courier := Actor{Kind: User, ID: uuid.NewString()}
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	f.mustTransition(t, o.ID, Assign, courier)
	beforePickup, err := f.repo.Get(f.ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.mustTransition(t, o.ID, Pickup, courier)
	s := NewDisposition(NewMutator(f.repo))
	if _, err := NewCanceller(
		f.repo,
		nil,
		AssignmentOptions{},
	).CancelConfirmed(f.ctx, Caller{ID: o.RequesterID}, o.ID, "", uuid.NewString(), beforePickup.Revision); !errors.Is(
		err,
		ErrConflict,
	) {
		t.Fatalf("stale confirmation after pickup: %v", err)
	}
	picked, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || picked.Status != Delivering {
		t.Fatalf("stale confirmation changed order: %+v %v", picked, err)
	}
	result, err := NewCanceller(
		f.repo,
		nil,
		AssignmentOptions{},
	).CancelConfirmed(f.ctx, Caller{ID: courier.ID, Suspended: true}, o.ID, "", uuid.NewString(), picked.Revision)
	cancelled := result.Order
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || !reflect.DeepEqual(cancelled, stored) || stored.Status != Failed || stored.ItemLocation != nil {
		t.Fatalf("cancellation did not persist independently of disposition: %+v %v", stored, err)
	}
	var outcomes, warnings int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FILTER (WHERE kind='order.outcome'), count(*) FILTER (WHERE kind='order.courier_warning_requested') FROM order_outbox WHERE order_id=$1", o.ID).
		Scan(&outcomes, &warnings); err != nil ||
		outcomes != 1 ||
		warnings != 1 {
		t.Fatalf("outcomes=%d warnings=%d error=%v", outcomes, warnings, err)
	}
	auditBefore, factsBefore, _ := f.counts(t, o.ID)
	const concurrency = 8
	errs := make([]error, concurrency)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = s.RecordItemLocation(f.ctx, Caller{ID: courier.ID}, o.ID, "Library desk")
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	auditAfter, factsAfter, _ := f.counts(t, o.ID)
	if auditAfter != auditBefore+1 || factsAfter != factsBefore {
		t.Fatalf("concurrent retries wrote audit %d->%d facts %d->%d", auditBefore, auditAfter, factsBefore, factsAfter)
	}
	corrected, err := s.RecordItemLocation(f.ctx, Caller{ID: courier.ID}, o.ID, "Residence reception")
	if err != nil {
		t.Fatal(err)
	}
	var originalEntries, correctedEntries int
	if err := f.pool.QueryRow(f.ctx, `SELECT count(*) FILTER (WHERE details->>'item_location'='Library desk'), count(*) FILTER (WHERE details->>'item_location'='Residence reception') FROM order_audit WHERE order_id=$1 AND action='record_item_location'`, o.ID).
		Scan(&originalEntries, &correctedEntries); err != nil ||
		originalEntries != 1 ||
		correctedEntries != 1 {
		t.Fatalf("correction audit lost: old=%d new=%d error=%v", originalEntries, correctedEntries, err)
	}
	// Reject the audit insert after the order UPDATE to prove both roll back.
	_, err = f.pool.Exec(
		f.ctx,
		`CREATE FUNCTION reject_disposition_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.action='record_item_location' THEN RAISE EXCEPTION 'injected disposition audit failure'; END IF; RETURN NEW; END $$;
CREATE TRIGGER reject_disposition_audit BEFORE INSERT ON order_audit FOR EACH ROW EXECUTE FUNCTION reject_disposition_audit();`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordItemLocation(f.ctx, Caller{ID: courier.ID}, o.ID, "Must roll back"); err == nil {
		t.Fatal("audit failure was acknowledged")
	}
	stored, err = f.repo.Get(f.ctx, o.ID)
	if err != nil || !reflect.DeepEqual(stored, corrected) {
		t.Fatalf("audit failure did not roll back disposition: %+v %v", stored, err)
	}
	if _, n, _ := f.counts(t, o.ID); n != factsBefore {
		t.Fatal("disposition emitted settlement or warning facts")
	}
	t.Log(
		"PASS: stale pickup confirmation rejected; cancellation and warning committed before location; eight concurrent disposition retries wrote once; correction audited; audit failure rolled back",
	)
}
