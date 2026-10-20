//go:build integration

package order

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Sources: R1.6.10, agreed Q1-Q9, and the explicit B2/B3 export contracts.
// PostgreSQL and the shared parent lock are real. Object storage/decoding use
// the existing explicit proof seam; these tests do not claim live S3 or Credit.
func TestPostgresDeliveryTimers(t *testing.T) {
	f := setup(t)
	prepare := func(t *testing.T) (*ProofReceipts, *proofDependencies, Order, Caller, ProofUpload) {
		t.Helper()
		f.reset(t)
		o := f.create(t)
		courier := Caller{ID: uuid.NewString()}
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		f.mustTransition(t, o.ID, Assign, Actor{Kind: User, ID: courier.ID})
		if _, err := NewProgress(NewMutator(f.repo)).ConfirmPickup(f.ctx, courier, o.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		d := &proofDependencies{
			repo: &proofMemory{},
			now:  &now,
			body: []byte("explicit decoder seam"),
		}
		proofs := NewProofs(f.repo, d, d, d, ProofOptions{UploadScope: "proof-primary"})
		row, _, err := proofs.CreateUpload(f.ctx, courier, ProofUploadInput{
			OrderID:     o.ID,
			UploadID:    uuid.NewString(),
			ContentType: "image/png",
			SizeBytes:   int64(len(d.body)),
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = proofs.VerifyUpload(f.ctx, courier, o.ID, row.ID); err != nil {
			t.Fatal(err)
		}
		return NewProofReceipts(proofs, f.repo), d, o, courier, row
	}
	setDeadline := func(t *testing.T, id string, offset string) {
		t.Helper()
		// Fixture adjustment only: preserve the real duration, including its
		// fractional microseconds, and compare against the database clock.
		if _, err := f.pool.Exec(f.ctx, `UPDATE orders SET picked_up_at=clock_timestamp()
   -ceil(delivery_duration_ns::numeric*2/1000)*interval '1 microsecond'
   +$2::interval WHERE id=$1`, id, offset); err != nil {
			t.Fatal(err)
		}
	}
	awaitDue := func(t *testing.T, id string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		defer cancel()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			var due bool
			if err := f.pool.QueryRow(ctx, `SELECT (EXTRACT(EPOCH FROM clock_timestamp())-EXTRACT(EPOCH FROM picked_up_at))*1000000000 >= delivery_duration_ns::numeric*2 FROM orders WHERE id=$1`, id).Scan(&due); err != nil {
				t.Fatal(err)
			}
			if due {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("deadline not reached", ctx.Err())
			case <-ticker.C:
			}
		}
	}
	counts := func(t *testing.T, id string) (int, int, int) {
		t.Helper()
		var audit, outcomes, warnings int
		if err := f.pool.QueryRow(f.ctx, `SELECT
   (SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='timeout'),
   (SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'),
   (SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.courier_warning_requested')`, id).Scan(&audit, &outcomes, &warnings); err != nil {
			t.Fatal(err)
		}
		return audit, outcomes, warnings
	}
	t.Run("fifty timeout contenders and late admission produce one failure", func(t *testing.T) {
		receipts, _, o, courier, proof := prepare(t)
		setDeadline(t, o.ID, "-1 second")
		before, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		failures := make(chan error, 50)
		for range 50 {
			wg.Go(func() { failures <- NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).Timeout(f.ctx, o.ID) })
		}
		_, admissionErr := receipts.ConfirmDelivery(f.ctx, courier, ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{proof.ID}})
		if !errors.Is(admissionErr, ErrPrecondition) {
			t.Fatalf("late admission: %v", admissionErr)
		}
		wg.Wait()
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != Failed || got.Revision != before.Revision+1 || got.DeliveredAt != nil || got.ClosedAt == nil || got.ReservedCredits != before.ReservedCredits {
			t.Fatalf("bad terminal timeout: %+v", got)
		}
		if a, n, w := counts(t, o.ID); a != 1 || n != 1 || w != 1 {
			t.Fatalf("duplicate/missing records: audit=%d outcome=%d warning=%d", a, n, w)
		}
		var payload []byte
		if err := f.pool.QueryRow(f.ctx, "SELECT payload FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).Scan(&payload); err != nil {
			t.Fatal(err)
		}
		var fact FactsPayload
		if err := json.Unmarshal(payload, &fact); err != nil {
			t.Fatal(err)
		}
		if fact.Action != Timeout || fact.Status != Failed || !fact.At.Equal(*got.ClosedAt) || fact.DeliveredAt != nil {
			t.Fatalf("wrong settlement observations: %+v", fact)
		}
		t.Log("50 timeout contenders + late confirmation -> one Failed revision, one System timeout audit, one outcome and one warning; no invented delivery/refund")
	})
	t.Run("on-time unavailable submission survives timeout and restart", func(t *testing.T) {
		receipts, d, o, courier, proof := prepare(t)
		setDeadline(t, o.ID, "5 seconds")
		d.unavailable = true
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{proof.ID}, Unattended: true}
		if _, err := receipts.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		var received time.Time
		if err := f.pool.QueryRow(f.ctx, "SELECT received_at FROM order_delivery_receipts WHERE order_id=$1 AND state='pending'", o.ID).Scan(&received); err != nil {
			t.Fatal(err)
		}
		awaitDue(t, o.ID)
		if err := NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != Delivering {
			t.Fatalf("pending submission timed out: %+v %v", got, err)
		}
		if a, n, w := counts(t, o.ID); a != 0 || n != 0 || w != 0 {
			t.Fatalf("pending generated terminal facts: %d %d %d", a, n, w)
		}
		d.unavailable = false
		restarted := NewProofReceipts(NewProofs(f.repo, d, d, d, ProofOptions{UploadScope: "proof-primary"}), f.repo)
		if err := restarted.RecoverOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		got, err = f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != Delivered || got.DeliveredAt == nil || !got.DeliveredAt.Equal(received) || !got.UpdatedAt.After(got.PickedUpAt.Add(2*got.DeliveryDuration)) {
			t.Fatalf("lost receipt/effective delivery on restart: %+v %v", got, err)
		}
		if _, err := NewProgress(NewMutator(f.repo)).ConfirmReceipt(f.ctx, Caller{ID: o.RequesterID}, o.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := restarted.ConfirmDelivery(f.ctx, courier, input); err != nil {
			t.Fatal(err)
		}
		if err := NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).ScanOnce(f.ctx); err != nil {
			t.Fatal(err)
		}
		if a, n, w := counts(t, o.ID); a != 0 || n != 1 || w != 0 {
			t.Fatalf("successful recovery also timed out: %d %d %d", a, n, w)
		}
		t.Log("preverified on-time receipt survives actual deadline and timer scan; fresh proof controller recovers original effective time; Completed replay succeeds; no timeout warning")
	})
	t.Run("invalid pending releases timeout without lending receipt", func(t *testing.T) {
		receipts, d, o, courier, proof := prepare(t)
		setDeadline(t, o.ID, "5 seconds")
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{proof.ID}}
		d.unavailable = true
		if _, err := receipts.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		awaitDue(t, o.ID)
		if err := NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).Timeout(f.ctx, o.ID); err != nil {
			t.Fatal(err)
		}
		protected, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || protected.Status != Delivering {
			t.Fatalf("pending invalid-proof scenario timed out before rejection: %+v %v", protected, err)
		}
		d.unavailable, d.invalid = false, true
		if _, err := receipts.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		if err := NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).Timeout(f.ctx, o.ID); err != nil {
			t.Fatal(err)
		}
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != Failed {
			t.Fatalf("rejected proof suppressed timeout: %+v %v", got, err)
		}
		d.invalid = false
		if _, err := receipts.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("rejected receipt revived: %v", err)
		}
		if a, n, w := counts(t, o.ID); a != 1 || n != 1 || w != 1 {
			t.Fatalf("bad timeout after rejection: %d %d %d", a, n, w)
		}
		t.Log("unavailable receipt protects order; definitive invalid proof releases protection; timeout commits once; rejected receipt cannot be replayed into delivery")
	})
	t.Run("SQL discovery and pending predicate agree at microsecond boundary", func(t *testing.T) {
		for _, extra := range []time.Duration{0, time.Nanosecond} {
			_, _, o, courier, proof := prepare(t)
			var at time.Time
			if err := f.pool.QueryRow(f.ctx, "SELECT clock_timestamp()::timestamptz").Scan(&at); err != nil {
				t.Fatal(err)
			}
			duration := 5*time.Minute + extra
			picked := at.Add(-10 * time.Minute)
			if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET picked_up_at=$2,delivery_duration_ns=$3 WHERE id=$1", o.ID, picked, int64(duration)); err != nil {
				t.Fatal(err)
			}
			ids, err := f.repo.DueDeliveryTimers(f.ctx, at, uuid.Nil.String(), deliveryTimerBatchSize)
			if err != nil {
				t.Fatal(err)
			}
			if (len(ids) == 1) != (extra == 0) {
				t.Fatalf("numeric discovery rounded fractional deadline early: extra=%s ids=%v", extra, ids)
			}
			ids, err = f.repo.DueDeliveryTimers(f.ctx, at.Add(time.Microsecond), uuid.Nil.String(), deliveryTimerBatchSize)
			if err != nil || len(ids) != 1 || ids[0] != o.ID {
				t.Fatalf("next representable instant did not discover deadline: %v %v", ids, err)
			}
			var pending bool
			if err := f.repo.WithinProofs(f.ctx, func(base ProofTx) error {
				if _, err := base.LockOrder(f.ctx, o.ID); err != nil {
					return err
				}
				if err := base.(ReceiptTx).InsertDeliveryReceipt(f.ctx, DeliveryReceipt{
					ID:         uuid.NewString(),
					OrderID:    o.ID,
					CourierID:  courier.ID,
					ProofIDs:   []string{proof.ID},
					ReceivedAt: at,
					State:      "pending",
				}); err != nil {
					return err
				}
				var err error
				pending, err = base.HasPendingDelivery(f.ctx, o.ID)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if pending != (extra != 0) {
				t.Fatalf("strict pending boundary disagrees: extra=%s pending=%v", extra, pending)
			}
			// Synthetic receipt timestamps isolate SQL precision, not live admission.
			if _, err := f.pool.Exec(f.ctx, "UPDATE order_delivery_receipts SET received_at=$2 WHERE order_id=$1", o.ID, at.Add(time.Microsecond)); err != nil {
				t.Fatal(err)
			}
			if err := f.repo.WithinProofs(f.ctx, func(base ProofTx) error {
				if _, err := base.LockOrder(f.ctx, o.ID); err != nil {
					return err
				}
				var err error
				pending, err = base.HasPendingDelivery(f.ctx, o.ID)
				return err
			}); err != nil || pending {
				t.Fatalf("late pending record suppressed timeout: %v %v", pending, err)
			}
		}
		t.Log("real SQL: equality is due/not eligible; +2ns deadline waits until next microsecond and protects only the prior representable receipt")
	})
	t.Run("on-time pending commit is visible after timer lock wait", func(t *testing.T) {
		_, _, o, courier, proof := prepare(t)
		setDeadline(t, o.ID, "5 seconds")
		held, release := make(chan struct{}), make(chan struct{})
		var once sync.Once
		unlock := func() { once.Do(func() { close(release) }) }
		defer unlock()
		admissionDone := make(chan error, 1)
		go func() {
			admissionDone <- f.repo.WithinProofs(f.ctx, func(base ProofTx) error {
				if _, err := base.LockOrder(f.ctx, o.ID); err != nil {
					return err
				}
				received, err := base.ProofServerTime(f.ctx)
				if err != nil {
					return err
				}
				tx := base.(ReceiptTx)
				if err := tx.InsertDeliveryReceipt(f.ctx, DeliveryReceipt{
					ID:         uuid.NewString(),
					OrderID:    o.ID,
					CourierID:  courier.ID,
					ProofIDs:   []string{proof.ID},
					ReceivedAt: received,
					State:      "pending",
				}); err != nil {
					return err
				}
				close(held)
				select {
				case <-release:
					return nil
				case <-f.ctx.Done():
					return f.ctx.Err()
				}
			})
		}()
		select {
		case <-held:
		case err := <-admissionDone:
			t.Fatalf("admission did not acquire lock: %v", err)
		case <-f.ctx.Done():
			t.Fatal(f.ctx.Err())
		}
		timeoutDone := make(chan error, 1)
		go func() { timeoutDone <- NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).Timeout(f.ctx, o.ID) }()
		ctx, cancel := context.WithTimeout(f.ctx, 10*time.Second)
		defer cancel()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			var waiting bool
			if err := f.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
    WHERE datname=current_database() AND wait_event_type='Lock'
    AND query LIKE '%FROM orders WHERE id = $1 FOR UPDATE%'
    AND cardinality(pg_blocking_pids(pid))>0)`).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("timer never waited on admission lock", ctx.Err())
			case <-ticker.C:
			}
		}
		awaitDue(t, o.ID)
		unlock()
		if err := <-admissionDone; err != nil {
			t.Fatal(err)
		}
		if err := <-timeoutDone; err != nil {
			t.Fatal(err)
		}
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != Delivering {
			t.Fatalf("stale pending snapshot timed out order: %+v %v", got, err)
		}
		if a, n, w := counts(t, o.ID); a != 0 || n != 0 || w != 0 {
			t.Fatalf("pending commit lost race: %d %d %d", a, n, w)
		}
		t.Log("timer waited on real PostgreSQL admission lock across deadline, then fresh pending read protected the order; no outcome/warning")
	})
	t.Run("cancellation wins over pending validation and timeout", func(t *testing.T) {
		receipts, d, o, courier, proof := prepare(t)
		d.unavailable = true
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{proof.ID}}
		if _, err := receipts.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		f.mustTransition(t, o.ID, Cancel, Actor{Kind: User, ID: o.RequesterID})
		if err := NewDeliveryTimers(f.repo, DeliveryTimerOptions{}).Timeout(f.ctx, o.ID); err != nil {
			t.Fatal(err)
		}
		d.unavailable = false
		if _, err := receipts.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		got, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || got.Status != Cancelled {
			t.Fatalf("terminal cancellation resurrected: %+v %v", got, err)
		}
		if a, n, w := counts(t, o.ID); a != 0 || n != 1 || w != 0 {
			t.Fatalf("cancel generated duplicate timeout: %d %d %d", a, n, w)
		}
		t.Log("requester cancellation defeats pending proof; timer writes no timeout; recovery cannot resurrect the order")
	})
}
