//go:build integration

package order

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

// Sources: R1.2.4/R1.6.11, atomic audit/outcome requirements, and the approved
// bounded scanning, restart/retry, original-deadline and closure-time behavior.
// This suite checks real PostgreSQL expiry, audit and outcomes, not remote
// Credit settlement. Delivery-confirmation races are checked in the delivery
// timer suite.
func TestPostgresExpiryTimers(t *testing.T) {
	f := setup(t)
	now := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	options := ExpiryTimerOptions{TestNow: func() time.Time { return now }}
	seed := func(t *testing.T, deadline time.Time) Order {
		t.Helper()
		o := f.create(t)
		if err := f.transition(o.ID, ConfirmReservation, Actor{Kind: System}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET expires_at=$2 WHERE id=$1", o.ID, deadline); err != nil {
			t.Fatal(err)
		}
		return o
	}

	t.Run("locked database clock prevents early expiry with fast discovery", func(t *testing.T) {
		f.reset(t)
		var serverNow time.Time
		if err := f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()::timestamptz").Scan(&serverNow); err != nil {
			t.Fatal(err)
		}
		o := seed(t, serverNow.Add(time.Hour))
		s := NewExpiryTimers(f.repo, ExpiryTimerOptions{})
		// Only candidate discovery is fast. Production decision time stays on the
		// transaction's database clock, with no TestNow override.
		s.lifecycleTimerScanner.now = func() time.Time { return serverNow.Add(365 * 24 * time.Hour) }
		if err := s.ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != AwaitingCourier {
			t.Fatalf("fast discovery prematurely expired request: %+v %v", got, err)
		}
		original := serverNow.Add(-20 * time.Minute)
		if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET expires_at=$2 WHERE id=$1", o.ID, original); err != nil {
			t.Fatal(err)
		}
		if err := s.ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		got, err = f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != Expired || got.ClosedAt == nil || got.ClosedAt.Before(serverNow) ||
			!got.ExpiresAt.Equal(original) {
			t.Fatalf("locked DB-clock expiry failed: %+v %v", got, err)
		}
		var after time.Time
		if err := f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()::timestamptz").Scan(&after); err != nil {
			t.Fatal(err)
		}
		if got.ClosedAt.After(after) {
			t.Fatal("closure used the fast discovery clock")
		}
		t.Log(
			"discovery one year fast cannot expire a future request; due request closes on actual locked DB time and preserves original expiry",
		)
	})
	t.Run("fifty workers commit one expiry outcome", func(t *testing.T) {
		f.reset(t)
		deadline := now.Add(-20 * time.Minute)
		o := seed(t, deadline)
		for _, status := range []OfferStatus{OfferOpen, OfferAccepting} {
			_, err := f.pool.Exec(
				f.ctx,
				`INSERT INTO offers (id,order_id,courier_id,credits,status,created_at,updated_at)
                VALUES ($1,$2,$3,$4,$5,$6,$6)`,
				uuid.NewString(),
				o.ID,
				uuid.NewString(),
				o.RewardCredits,
				status,
				o.CreatedAt,
			)
			if err != nil {
				t.Fatal(err)
			}
		}
		var workers sync.WaitGroup
		failures := make(chan error, 50)
		for range 50 {
			workers.Go(func() {
				failures <- NewExpiryTimers(f.repo, options).Expire(f.ctx, o.ID)
			})
		}
		workers.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		row, err := db.New(f.pool).GetOrder(f.ctx, uuid.MustParse(o.ID))
		if err != nil {
			t.Fatal(err)
		}
		if row.Status != string(Expired) || row.Revision != 3 || row.ClosedAt == nil || !row.ClosedAt.Equal(now) ||
			!row.ExpiresAt.Equal(deadline) ||
			row.ReservedCredits != o.ReservedCredits {
			t.Fatalf("wrong expiry observations: %+v", row)
		}
		var audits, outcomes, systemActors int
		if err := f.pool.QueryRow(f.ctx, `SELECT
			(SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='expire'),
			(SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'),
			(SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='expire' AND actor_kind='system' AND actor_id IS NULL)`, o.ID).Scan(&audits, &outcomes, &systemActors); err != nil {
			t.Fatal(err)
		}
		if audits != 1 || outcomes != 1 || systemActors != 1 {
			t.Fatalf(
				"duplicate or missing durable records: audits=%d outcomes=%d system=%d",
				audits,
				outcomes,
				systemActors,
			)
		}
		var voidOffers int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM offers WHERE order_id=$1 AND status='void'", o.ID).
			Scan(&voidOffers); err != nil ||
			voidOffers != 2 {
			t.Fatalf("active offer voiding not committed with expiry: %d %v", voidOffers, err)
		}
		t.Log(
			"50 expiry contenders -> one revision, one System expiry audit, one terminal outcome; original expiry/reservation retained and active offers voided",
		)
	})

	t.Run("failed first candidate cannot starve a later page", func(t *testing.T) {
		f.reset(t)
		for range expiryTimerBatchSize + 2 {
			seed(t, now)
		}
		ids, err := f.repo.DueExpiryTimers(f.ctx, now, uuid.Nil.String(), expiryTimerBatchSize)
		if err != nil || len(ids) != expiryTimerBatchSize {
			t.Fatalf("bounded scan: %v %d", err, len(ids))
		}
		broken := ids[0]
		_, err = f.pool.Exec(f.ctx, fmt.Sprintf(`CREATE FUNCTION reject_expiry() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN
			IF NEW.id = '%s'::uuid AND NEW.status = 'expired' THEN RAISE EXCEPTION 'injected expiry failure'; END IF;
			RETURN NEW;
		END $$;
		CREATE TRIGGER reject_expiry BEFORE UPDATE ON orders FOR EACH ROW EXECUTE FUNCTION reject_expiry();`, broken))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if _, err := f.pool.Exec(
				context.Background(),
				"DROP TRIGGER IF EXISTS reject_expiry ON orders; DROP FUNCTION IF EXISTS reject_expiry()",
			); err != nil {
				t.Error(err)
			}
		})
		s := NewExpiryTimers(f.repo, options)
		if err := s.ScanOnce(f.ctx); err != nil {
			t.Fatalf("one failed candidate stopped discovery: %v", err)
		}
		if err := s.ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		var expired, brokenOutcomes int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM orders WHERE status='expired'").
			Scan(&expired); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", broken).
			Scan(&brokenOutcomes); err != nil {
			t.Fatal(err)
		}
		if expired != expiryTimerBatchSize+1 || brokenOutcomes != 0 {
			t.Fatalf(
				"starved backlog or partial failed outcome: expired=%d broken_outcomes=%d",
				expired,
				brokenOutcomes,
			)
		}
		if _, err := f.pool.Exec(
			f.ctx,
			"DROP TRIGGER reject_expiry ON orders; DROP FUNCTION reject_expiry()",
		); err != nil {
			t.Fatal(err)
		}
		if err := NewExpiryTimers(f.repo, options).ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM orders WHERE status='expired'").
			Scan(&expired); err != nil ||
			expired != expiryTimerBatchSize+2 {
			t.Fatalf("restart did not recover failed work: %d %v", expired, err)
		}
		t.Log(
			"52 overdue orders -> failure on first candidate rolls back; next page expires; new worker recovers the failed order",
		)
	})

	t.Run("withdrawal and unassignment retain original expiry", func(t *testing.T) {
		for _, action := range []Action{Withdraw, Unassign} {
			f.reset(t)
			deadline := now.Add(-time.Minute)
			o := seed(t, deadline)
			courier := uuid.NewString()
			f.mustTransition(t, o.ID, Assign, Actor{Kind: User, ID: courier})
			actor := Actor{Kind: User, ID: courier}
			if action == Unassign {
				actor = Actor{Kind: Admin, ID: uuid.NewString()}
			}
			f.mustTransition(t, o.ID, action, actor)
			if err := NewExpiryTimers(f.repo, options).Expire(f.ctx, o.ID); err != nil {
				t.Fatal(err)
			}
			row, err := db.New(f.pool).GetOrder(f.ctx, uuid.MustParse(o.ID))
			if err != nil || row.Status != string(Expired) || !row.ExpiresAt.Equal(deadline) || row.CourierID != nil {
				t.Fatalf("%s extended expiry or retained assignment: %+v %v", action, row, err)
			}
		}
		t.Log(
			"local withdrawal/unassignment preserve original expiry; overdue reopened orders expire without a renewed window (financial reset saga is external)",
		)
	})

	t.Run("cancellation racing expiry records only one outcome", func(t *testing.T) {
		f.reset(t)
		o := seed(t, now)
		start := make(chan struct{})
		cancelled, expired := make(chan error, 1), make(chan error, 1)
		go func() { <-start; cancelled <- f.transition(o.ID, Cancel, Actor{Kind: User, ID: o.RequesterID}) }()
		go func() { <-start; expired <- NewExpiryTimers(f.repo, options).Expire(f.ctx, o.ID) }()
		close(start)
		if err := <-cancelled; err != nil && !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		if err := <-expired; err != nil {
			t.Fatal(err)
		}
		row, err := db.New(f.pool).GetOrder(f.ctx, uuid.MustParse(o.ID))
		if err != nil || (row.Status != string(Cancelled) && row.Status != string(Expired)) || row.Revision != 3 {
			t.Fatalf("conflicting terminal changes: %+v %v", row, err)
		}
		var outcomes int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).
			Scan(&outcomes); err != nil ||
			outcomes != 1 {
			t.Fatalf("conflicting outcomes: %d %v", outcomes, err)
		}
		t.Log("cancellation versus expiry -> one terminal revision and one outcome")
	})

	t.Run("assignment committed while expiry waits wins", func(t *testing.T) {
		f.reset(t)
		o := seed(t, now)
		locked, release := make(chan struct{}), make(chan struct{})
		assignmentDone := make(chan error, 1)
		go func() {
			assignmentDone <- f.repo.Within(f.ctx, func(tx Tx) error {
				before, err := tx.LockOrder(f.ctx, o.ID)
				if err != nil {
					return err
				}
				if _, err := tx.Relationships(f.ctx, o.ID); err != nil {
					return err
				}
				courier := uuid.NewString()
				after := cloneOrder(before)
				after.Status, after.CourierID = AwaitingPickup, &courier
				assignedAt := now.Add(-time.Second)
				after.AssignedAt, after.UpdatedAt = &assignedAt, assignedAt
				after.Revision++
				change, err := NewChange(&before, after, Actor{Kind: User, ID: courier}, Assign, nil, uuid.NewString)
				if err != nil {
					return err
				}
				if err := tx.Apply(f.ctx, change); err != nil {
					return err
				}
				close(locked)
				select {
				case <-release:
					return nil
				case <-f.ctx.Done():
					return f.ctx.Err()
				}
			})
		}()
		select {
		case <-locked:
		case err := <-assignmentDone:
			t.Fatalf("assignment failed before holding lock: %v", err)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
		var once sync.Once
		unlock := func() { once.Do(func() { close(release) }) }
		defer unlock()
		expiryDone := make(chan error, 1)
		go func() { expiryDone <- NewExpiryTimers(f.repo, options).Expire(f.ctx, o.ID) }()
		waitCtx, cancel := context.WithTimeout(f.ctx, 5*time.Second)
		defer cancel()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err := f.pool.QueryRow(waitCtx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
				WHERE datname=current_database() AND wait_event_type='Lock'
				AND query LIKE '%FROM orders WHERE id = $1 FOR UPDATE%'
				AND cardinality(pg_blocking_pids(pid)) > 0)`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case <-waitCtx.Done():
				t.Fatal("expiry never waited on the assignment lock")
			case <-ticker.C:
			}
		}
		unlock()
		if err := <-assignmentDone; err != nil {
			t.Fatal(err)
		}
		if err := <-expiryDone; err != nil {
			t.Fatal(err)
		}
		row, err := db.New(f.pool).GetOrder(f.ctx, uuid.MustParse(o.ID))
		if err != nil || row.Status != string(AwaitingPickup) || row.Revision != 3 {
			t.Fatalf("expiry ignored newly committed assignment: %+v %v", row, err)
		}
		var outcomes int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).
			Scan(&outcomes); err != nil ||
			outcomes != 0 {
			t.Fatalf("assignment also produced expiry: %d %v", outcomes, err)
		}
		t.Log("expiry waited on a real PostgreSQL row lock, saw the committed assignment, and wrote no outcome")
	})
}
