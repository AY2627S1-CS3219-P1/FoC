//go:build integration

package reservation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/testsupport/postgres"
	"github.com/google/uuid"
)

func TestPostgresReservations(t *testing.T) {
	pool := postgres.Setup(t)
	clock := func() time.Time { return time.Now().UTC() }
	s := reservation.NewService(reservation.NewPostgresRepository(pool), clock, reservation.LatenessPayout)
	accounts := account.NewService(account.NewPostgresStore(pool), nil, clock)
	ctx := context.Background()
	t.Cleanup(func() { postgres.CheckLedger(t, pool) })

	balance := func(t *testing.T, id string) account.Account {
		t.Helper()
		a, err := accounts.GetAccount(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	t.Run("completed order pays the courier and refunds the rest", func(t *testing.T) {
		order, req, cour := uuid.NewString(), uuid.NewString(), uuid.NewString()
		offer := uuid.NewString()
		must(t)(s.Reserve(ctx, order, req, 30))
		must(t)(s.AdjustReservation(ctx, order, offer, cour, 30))
		picked := time.Date(2026, 10, 20, 9, 0, 0, 123456789, time.UTC)
		r, applied, err := s.ApplyOutcome(ctx, reservation.OrderOutcome{
			OrderID: order, Type: reservation.OrderCompleted, RequesterID: req, CourierID: cour,
			PickedUpAt: picked, DeliveredAt: picked.Add(90 * time.Minute), DeliveryDuration: time.Hour,
		})
		if err != nil || !applied {
			t.Fatalf("apply: %v %v", applied, err)
		}
		if r.Settlement.PaidToCourier != 15 || r.Settlement.ReturnedToRequester != 15 {
			t.Fatalf("settlement: %+v", r.Settlement)
		}
		got := must(t)(s.GetReservation(ctx, order))
		c := got.Settlement.Completion
		if c == nil || !c.PickedUpAt.Equal(picked.Truncate(time.Microsecond)) || c.DeliveryDuration != time.Hour {
			t.Fatalf("stored completion: %+v", c)
		}
		if a := balance(t, req); a.Balance != 85 || a.Reserved != 0 {
			t.Fatalf("requester: %+v", a)
		}
		if a := balance(t, cour); a.Balance != 115 {
			t.Fatalf("courier: %+v", a)
		}
		if _, applied, err := s.ApplyOutcome(ctx, reservation.OrderOutcome{
			OrderID: order, Type: reservation.OrderCompleted, RequesterID: req, CourierID: cour,
			PickedUpAt: picked, DeliveredAt: picked, DeliveryDuration: time.Hour,
		}); err != nil || applied {
			t.Fatalf("duplicate: %v %v", applied, err)
		}
	})

	t.Run("replays return the snapshot each call produced until fenced", func(t *testing.T) {
		order, req, cour := uuid.NewString(), uuid.NewString(), uuid.NewString()
		offer := uuid.NewString()
		reserved := must(t)(s.Reserve(ctx, order, req, 20))
		adjusted := must(t)(s.AdjustReservation(ctx, order, offer, cour, 40))
		r := must(t)(s.Reserve(ctx, order, req, 20))
		if r.Status != reservation.Held || r.Revision != 1 || !sameStamps(r, reserved) {
			t.Fatalf("reserve replay: %+v, first %+v", r, reserved)
		}
		r = must(t)(s.AdjustReservation(ctx, order, offer, cour, 40))
		if r.Status != reservation.Assigned || r.ReservedAmount != 40 || r.Adjustment == nil || r.Adjustment.CourierID != cour {
			t.Fatalf("adjust replay: %+v", r)
		}
		if r.Revision != 2 || !sameStamps(r, adjusted) {
			t.Fatalf("adjust replay: %+v, first %+v", r, adjusted)
		}

		must(t)(s.RevertAdjustment(ctx, order, offer))
		_, err := s.AdjustReservation(ctx, order, offer, cour, 40)
		expect(t, err, reservation.ErrAdjustmentReverted)

		must(t)(s.ReleaseReservation(ctx, order, reservation.Failed))
		_, err = s.Reserve(ctx, order, req, 20)
		expect(t, err, reservation.ErrReservationReleased)
		_, err = s.AdjustReservation(ctx, order, offer, cour, 40)
		expect(t, err, reservation.ErrReservationReleased)
		r = must(t)(s.ReleaseReservation(ctx, order, reservation.Failed))
		if r.Status != reservation.Released || r.Settlement.ReturnedToRequester != 20 {
			t.Fatalf("release replay: %+v", r.Settlement)
		}
		_, err = s.ReleaseReservation(ctx, order, reservation.Expired)
		expect(t, err, reservation.ErrIdempotencyConflict)
	})

	t.Run("stored rejection and tombstones", func(t *testing.T) {
		order, req := uuid.NewString(), uuid.NewString()
		_, err := s.Reserve(ctx, order, req, 101)
		expect(t, err, reservation.ErrInsufficientCredits)
		_, err = s.Reserve(ctx, order, req, 101)
		expect(t, err, reservation.ErrInsufficientCredits)
		released := uuid.NewString()
		must(t)(s.ReleaseReservation(ctx, released, reservation.Expired))
		_, err = s.Reserve(ctx, released, req, 10)
		expect(t, err, reservation.ErrReservationReleased)

		held, offer := uuid.NewString(), uuid.NewString()
		must(t)(s.Reserve(ctx, held, req, 10))
		must(t)(s.RevertAdjustment(ctx, held, offer))
		_, err = s.AdjustReservation(ctx, held, offer, uuid.NewString(), 10)
		expect(t, err, reservation.ErrAdjustmentReverted)
	})

	t.Run("cancellation after pickup pays the whole offer", func(t *testing.T) {
		order, req, cour := uuid.NewString(), uuid.NewString(), uuid.NewString()
		must(t)(s.Reserve(ctx, order, req, 25))
		must(t)(s.AdjustReservation(ctx, order, uuid.NewString(), cour, 25))
		r, applied, err := s.ApplyOutcome(ctx, reservation.OrderOutcome{
			OrderID: order, Type: reservation.OrderCancelledAfterPickup, RequesterID: req, CourierID: cour,
		})
		if err != nil || !applied || r.Settlement.PaidToCourier != 25 || r.Settlement.Completion != nil {
			t.Fatalf("apply: %+v %v %v", r, applied, err)
		}
		_, _, err = s.ApplyOutcome(ctx, reservation.OrderOutcome{OrderID: order, Type: reservation.OrderFailed, RequesterID: req})
		expect(t, err, reservation.ErrOutcomeMismatch)
	})

	t.Run("concurrent identical reserves hold once", func(t *testing.T) {
		order, req := uuid.NewString(), uuid.NewString()
		errs := concurrently(10, func() error {
			_, err := s.Reserve(ctx, order, req, 40)
			return err
		})
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if a := balance(t, req); a.Reserved != 40 || a.Balance != 100 {
			t.Fatalf("requester: %+v", a)
		}
	})

	t.Run("concurrent reserves never overdraw", func(t *testing.T) {
		req := uuid.NewString()
		errs := concurrently(10, func() error {
			_, err := s.Reserve(ctx, uuid.NewString(), req, 30)
			return err
		})
		ok := 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case !errors.Is(err, reservation.ErrInsufficientCredits):
				t.Fatal(err)
			}
		}
		if a := balance(t, req); ok != 3 || a.Reserved != 90 {
			t.Fatalf("%d succeeded; requester: %+v", ok, a)
		}
	})

	t.Run("concurrent first reserves grant once", func(t *testing.T) {
		user := uuid.NewString()
		errs := concurrently(10, func() error {
			_, err := s.Reserve(ctx, uuid.NewString(), user, 10)
			return err
		})
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}

		var grants int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM ledger_entries WHERE user_id = $1 AND kind = 'GRANT'`, user).
			Scan(&grants); err != nil || grants != 1 {
			t.Fatalf("grants: %d %v", grants, err)
		}
		if a := balance(t, user); a.Balance != 100 || a.Reserved != 100 {
			t.Fatalf("account: %+v", a)
		}
	})

	t.Run("reads never open an account", func(t *testing.T) {
		user := uuid.NewString()
		if _, err := accounts.GetAccount(ctx, user); !errors.Is(err, account.ErrAccountNotFound) {
			t.Fatalf("get: %v", err)
		}
		if _, err := accounts.ListLedgerEntries(ctx, user, account.Page{}); !errors.Is(err, account.ErrAccountNotFound) {
			t.Fatalf("list: %v", err)
		}

		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE user_id = $1)`, user).
			Scan(&exists); err != nil || exists {
			t.Fatalf("account opened by a read: %v %v", exists, err)
		}
	})

	t.Run("database assigns revisions on update", func(t *testing.T) {
		order, req := uuid.NewString(), uuid.NewString()
		must(t)(s.Reserve(ctx, order, req, 10))
		if _, err := pool.Exec(ctx, `UPDATE reservations SET revision = 1 WHERE order_id = $1`, order); err != nil {
			t.Fatal(err)
		}
		if r := must(t)(s.GetReservation(ctx, order)); r.Revision != 2 {
			t.Fatalf("reservation revision: %d", r.Revision)
		}
		var revision int64
		if err := pool.QueryRow(ctx, `SELECT revision FROM accounts WHERE user_id = $1`, req).Scan(&revision); err != nil || revision != 3 {
			t.Fatalf("account revision after grant and reserve: %d %v", revision, err)
		}
	})

	t.Run("terminal reservations are immutable in the database", func(t *testing.T) {
		order := uuid.NewString()
		must(t)(s.ReleaseReservation(ctx, order, reservation.Expired))
		if _, err := pool.Exec(ctx, `UPDATE reservations SET release_reason = 'FAILED' WHERE order_id = $1`, order); err == nil {
			t.Fatal("terminal reservation changed")
		}
	})

	t.Run("ledger lists newest first", func(t *testing.T) {
		req := uuid.NewString()
		order := uuid.NewString()
		must(t)(s.Reserve(ctx, order, req, 10))
		must(t)(s.ReleaseReservation(ctx, order, reservation.CancelledBeforePickup))
		page, err := accounts.ListLedgerEntries(ctx, req, account.Page{Size: 2})
		if err != nil {
			t.Fatal(err)
		}
		kinds := []account.Kind{page.Entries[0].Kind, page.Entries[1].Kind}
		if page.PageInfo.TotalItems != 3 || page.PageInfo.TotalPages != 2 || kinds[0] != account.Release || kinds[1] != account.Reserve ||
			page.Entries[0].OrderID != order {
			t.Fatalf("page: %+v", page)
		}
	})
}

// sameStamps reports whether a and b carry the same stored revision and times.
func sameStamps(a, b reservation.Reservation) bool {
	return a.Revision == b.Revision && a.CreatedAt.Equal(b.CreatedAt) && a.UpdatedAt.Equal(b.UpdatedAt)
}

func concurrently(n int, f func() error) []error {
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		wg.Go(func() {
			<-start
			errs[i] = f()
		})
	}
	close(start)
	wg.Wait()
	return errs
}
