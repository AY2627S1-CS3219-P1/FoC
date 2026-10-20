//go:build integration

package order

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestPostgresProgress(t *testing.T) {
	f := setup(t)
	progress := NewProgress(NewMutator(f.repo))
	assigned := func(t *testing.T) (Order, Actor) {
		t.Helper()
		f.reset(t)
		o := f.create(t)
		courier := Actor{User, uuid.NewString()}
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		f.mustTransition(t, o.ID, Assign, courier)
		return o, courier
	}

	t.Run("pickup then receipt persist what they return", func(t *testing.T) {
		o, courier := assigned(t)
		picked, err := progress.ConfirmPickup(f.ctx, Caller{ID: courier.ID}, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(picked, stored) || stored.Status != Delivering || stored.PickedUpAt == nil {
			t.Fatalf("pickup response and store differ\n%+v\n%+v", picked, stored)
		}

		repeat, err := progress.ConfirmPickup(f.ctx, Caller{ID: courier.ID}, o.ID)
		if err != nil || !reflect.DeepEqual(repeat, picked) {
			t.Fatalf("pickup repeat = %+v, %v", repeat, err)
		}

		f.mustTransition(t, o.ID, Deliver, courier)
		requester := Caller{ID: o.RequesterID}
		completed, err := progress.ConfirmReceipt(f.ctx, requester, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored, _ = f.repo.Get(f.ctx, o.ID); !reflect.DeepEqual(completed, stored) || stored.Status != Completed {
			t.Fatalf("receipt response and store differ\n%+v\n%+v", completed, stored)
		}
		audit, outbox, _ := f.counts(t, o.ID)

		repeat, err = progress.ConfirmReceipt(f.ctx, requester, o.ID)
		if err != nil || !reflect.DeepEqual(repeat, completed) {
			t.Fatalf("repeat = %+v, %v", repeat, err)
		}
		if a, n, _ := f.counts(t, o.ID); a != audit || n != outbox {
			t.Fatalf("repeat wrote audit %d->%d outbox %d->%d", audit, a, outbox, n)
		}
		var outcomes int
		if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).Scan(&outcomes); err != nil || outcomes != 1 {
			t.Fatalf("outcomes = %d, %v", outcomes, err)
		}
	})

	t.Run("concurrent pickups write once and all succeed", func(t *testing.T) {
		o, courier := assigned(t)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, errs[i] = progress.ConfirmPickup(f.ctx, Caller{ID: courier.ID}, o.ID)
			}()
		}
		wg.Wait()

		for _, err := range errs {
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		}
		var pickups int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='pickup'", o.ID).Scan(&pickups); err != nil {
			t.Fatal(err)
		}
		if pickups != 1 {
			t.Fatalf("pickup audits = %d", pickups)
		}
	})

	t.Run("strangers cannot learn an assigned order exists", func(t *testing.T) {
		o, _ := assigned(t)
		if _, err := progress.ConfirmPickup(f.ctx, Caller{ID: uuid.NewString()}, o.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("err = %v", err)
		}
		if _, err := progress.ConfirmPickup(f.ctx, Caller{ID: uuid.NewString()}, uuid.NewString()); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing order err = %v", err)
		}
	})
}
