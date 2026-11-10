//go:build integration

package order

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// A delivered order the requester never confirms completes on its own after
// ReceiptWindow, as the system, with one outcome for Credit to settle.
func TestPostgresReceiptTimers(t *testing.T) {
	f := setup(t)
	delivered := func(t *testing.T) Order {
		t.Helper()
		f.reset(t)
		o := f.create(t)
		courier := Actor{Kind: User, ID: uuid.NewString()}
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		f.mustTransition(t, o.ID, Assign, courier)
		f.mustTransition(t, o.ID, Pickup, courier)
		f.mustTransition(t, o.ID, Deliver, courier)
		return o
	}
	deliveredAgo := func(t *testing.T, id, ago string) {
		t.Helper()
		if _, err := f.pool.Exec(f.ctx,
			`UPDATE orders SET delivered_at = clock_timestamp() - $2::interval WHERE id = $1`, id, ago); err != nil {
			t.Fatal(err)
		}
	}
	counts := func(t *testing.T, id string) (audit, outcomes int) {
		t.Helper()
		if err := f.pool.QueryRow(f.ctx, `SELECT
   (SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='auto_complete' AND actor_kind='system'),
   (SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome')`, id).Scan(&audit, &outcomes); err != nil {
			t.Fatal(err)
		}
		return audit, outcomes
	}
	scan := func(t *testing.T) {
		t.Helper()
		if err := NewReceiptTimers(f.repo, ReceiptTimerOptions{}).ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("completes after the receipt window", func(t *testing.T) {
		o := delivered(t)
		deliveredAgo(t, o.ID, "61 minutes")
		scan(t)
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != Completed || !got.AutomaticallyCompleted || got.CompletedAt == nil || got.DeliveredAt == nil ||
			got.PickedUpAt == nil {
			t.Fatalf("order = %+v", got)
		}
		auditBefore, outboxBefore, _ := f.counts(t, o.ID)
		_, err = NewProgress(NewMutator(f.repo)).ConfirmReceipt(f.ctx, Caller{ID: o.RequesterID}, o.ID)
		if !errors.Is(err, ErrPrecondition) {
			t.Fatalf("confirmation after automatic completion: %v", err)
		}
		stored, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || !reflect.DeepEqual(stored, got) {
			t.Fatalf("late confirmation changed order: %+v, %v", stored, err)
		}
		if a, n, _ := f.counts(t, o.ID); a != auditBefore || n != outboxBefore {
			t.Fatalf("late confirmation wrote audit/outbox: %d/%d -> %d/%d", auditBefore, outboxBefore, a, n)
		}
		if audit, outcomes := counts(t, o.ID); audit != 1 || outcomes != 1 {
			t.Fatalf("audit=%d outcomes=%d", audit, outcomes)
		}
	})

	t.Run("waits inside the receipt window", func(t *testing.T) {
		o := delivered(t)
		deliveredAgo(t, o.ID, "59 minutes")
		scan(t)
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != Delivered {
			t.Fatalf("status = %s", got.Status)
		}
	})

	t.Run("a requester's confirmation wins", func(t *testing.T) {
		o := delivered(t)
		f.mustTransition(t, o.ID, ConfirmReceipt, Actor{Kind: User, ID: o.RequesterID})
		deliveredAgo(t, o.ID, "2 hours")
		if err := NewReceiptTimers(f.repo, ReceiptTimerOptions{}).Complete(f.ctx, o.ID); err != nil {
			t.Fatal(err)
		}
		if audit, outcomes := counts(t, o.ID); audit != 0 || outcomes != 1 {
			t.Fatalf("audit=%d outcomes=%d", audit, outcomes)
		}
	})

	t.Run("concurrent timers complete once", func(t *testing.T) {
		o := delivered(t)
		deliveredAgo(t, o.ID, "2 hours")
		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for range 20 {
			wg.Go(func() { errs <- NewReceiptTimers(f.repo, ReceiptTimerOptions{}).Complete(f.ctx, o.ID) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if audit, outcomes := counts(t, o.ID); audit != 1 || outcomes != 1 {
			t.Fatalf("audit=%d outcomes=%d", audit, outcomes)
		}
	})
}
