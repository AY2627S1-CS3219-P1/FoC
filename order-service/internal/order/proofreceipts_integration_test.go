//go:build integration

package order

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// These outcomes come from the approved receipt-time/pending/cancellation
// contract, not from the adapter's implementation. Storage is an explicit seam.
func TestPostgresProofReceipts(t *testing.T) {
	f := setup(t)
	prepare := func(t *testing.T) (*Proofs, *ProofReceipts, *proofDependencies, Order, Caller, ProofUpload) {
		t.Helper()
		f.reset(t)
		o := f.create(t)
		courier := Caller{ID: uuid.NewString()}
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
		f.mustTransition(t, o.ID, Assign, Actor{Kind: User, ID: courier.ID})
		progress := NewProgress(NewMutator(f.repo))
		if _, err := progress.ConfirmPickup(f.ctx, courier, o.ID); err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		d := &proofDependencies{repo: &proofMemory{}, now: &now, body: []byte("decoder seam")}
		proofs := NewProofs(f.repo, d, d, d, ProofOptions{UploadScope: "proof-primary"})
		row, _, err := proofs.CreateUpload(f.ctx, courier, ProofUploadInput{OrderID: o.ID, UploadID: uuid.NewString(), ContentType: "image/png", SizeBytes: int64(len(d.body))})
		if err != nil {
			t.Fatal(err)
		}
		if err = proofs.VerifyUpload(f.ctx, courier, o.ID, row.ID); err != nil {
			t.Fatal(err)
		}
		return proofs, NewProofReceipts(proofs, f.repo), d, o, courier, row
	}
	t.Run("pending is durable and blocks timeout until validation finishes", func(t *testing.T) {
		_, service, d, o, courier, row := prepare(t)
		d.unavailable = true
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}, Unattended: true}
		if _, err := service.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		var received time.Time
		if err := f.pool.QueryRow(f.ctx, "SELECT received_at FROM order_delivery_receipts WHERE order_id=$1 AND state='pending'", o.ID).Scan(&received); err != nil {
			t.Fatal(err)
		}
		if err := f.repo.WithinProofs(f.ctx, func(tx ProofTx) error {
			if _, err := tx.LockOrder(f.ctx, o.ID); err != nil {
				return err
			}
			pending, err := tx.HasPendingDelivery(f.ctx, o.ID)
			if err == nil && !pending {
				t.Fatal("durable pending did not block timeout")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		// A fresh service/controller recovers the same persisted receipt, not a new
		// effective time. No in-process queue is required to preserve eligibility.
		d.unavailable = false
		restarted := NewProofReceipts(service.proofs, f.repo)
		delivered, err := restarted.ConfirmDelivery(f.ctx, courier, input)
		if err != nil {
			t.Fatal(err)
		}
		if delivered.DeliveredAt == nil || !delivered.DeliveredAt.Equal(received) || delivered.UpdatedAt.Before(received) {
			t.Fatalf("effective/audit times: %+v", delivered)
		}
		if _, err = NewProgress(NewMutator(f.repo)).ConfirmReceipt(f.ctx, Caller{ID: o.RequesterID}, o.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = restarted.ConfirmDelivery(f.ctx, courier, input); err != nil {
			t.Fatal(err)
		}
		var n int
		if err = f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='deliver'", o.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("delivery audit count %d: %v", n, err)
		}
	})
	t.Run("cancelled pending cannot resurrect the order", func(t *testing.T) {
		_, service, d, o, courier, row := prepare(t)
		d.unavailable = true
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}
		if _, err := service.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		f.mustTransition(t, o.ID, Cancel, Actor{Kind: User, ID: o.RequesterID})
		d.unavailable = false
		if _, err := service.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		stored, err := f.repo.Get(f.ctx, o.ID)
		if err != nil || stored.Status != Cancelled {
			t.Fatalf("resurrected %+v: %v", stored, err)
		}
		var state string
		if err = f.pool.QueryRow(f.ctx, "SELECT state FROM order_delivery_receipts WHERE order_id=$1", o.ID).Scan(&state); err != nil || state != "superseded" {
			t.Fatalf("receipt=%s %v", state, err)
		}
	})
	t.Run("preexisting verification required and pending evidence cannot be removed", func(t *testing.T) {
		proofs, service, d, o, courier, row := prepare(t)
		late, _, err := proofs.CreateUpload(f.ctx, courier, ProofUploadInput{OrderID: o.ID, UploadID: uuid.NewString(), ContentType: "image/png", SizeBytes: int64(len(d.body))})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = service.ConfirmDelivery(f.ctx, courier, ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{late.ID}}); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		d.unavailable = true
		if _, err = service.ConfirmDelivery(f.ctx, courier, ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if err = proofs.AbandonUpload(f.ctx, courier, o.ID, row.UploadID); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		// A client nonce matching the server ID is a separate fenced identity.
		if err = proofs.AbandonUpload(f.ctx, courier, o.ID, row.ID); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("invalid submission releases timeout eligibility without transferring receipt", func(t *testing.T) {
		_, service, d, o, courier, row := prepare(t)
		d.invalid = true
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}
		if _, err := service.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		if err := f.repo.WithinProofs(f.ctx, func(tx ProofTx) error {
			if _, err := tx.LockOrder(f.ctx, o.ID); err != nil {
				return err
			}
			pending, err := tx.HasPendingDelivery(f.ctx, o.ID)
			if pending {
				t.Fatal("invalid proof still blocks timeout")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_delivery_receipts WHERE order_id=$1 AND state='rejected'", o.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("rejections=%d %v", n, err)
		}
	})
	t.Run("on-time pending remains eligible after the strict deadline", func(t *testing.T) {
		_, service, d, o, courier, row := prepare(t)
		if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET picked_up_at=clock_timestamp()-((delivery_duration_ns*2)/1000)*interval '1 microsecond'+interval '2 seconds' WHERE id=$1", o.ID); err != nil {
			t.Fatal(err)
		}
		d.unavailable = true
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}
		if _, err := service.ConfirmDelivery(f.ctx, courier, input); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		var received time.Time
		if err := f.pool.QueryRow(f.ctx, "SELECT received_at FROM order_delivery_receipts WHERE order_id=$1", o.ID).Scan(&received); err != nil {
			t.Fatal(err)
		}
		timer := time.NewTimer(2100 * time.Millisecond)
		defer timer.Stop()
		<-timer.C
		d.unavailable = false
		delivered, err := service.ConfirmDelivery(f.ctx, courier, input)
		if err != nil || delivered.DeliveredAt == nil || !delivered.DeliveredAt.Equal(received) {
			t.Fatalf("lost on-time eligibility: %+v %v", delivered, err)
		}
		if !delivered.UpdatedAt.After(delivered.PickedUpAt.Add(2 * delivered.DeliveryDuration)) {
			t.Fatal("scenario did not finish after deadline")
		}
	})
	t.Run("late first receipt never becomes pending", func(t *testing.T) {
		_, service, _, o, courier, row := prepare(t)
		if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET picked_up_at=clock_timestamp()-((delivery_duration_ns*2)/1000)*interval '1 microsecond' WHERE id=$1", o.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := service.ConfirmDelivery(f.ctx, courier, ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}); !errors.Is(err, ErrPrecondition) {
			t.Fatal(err)
		}
		var n int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_delivery_receipts WHERE order_id=$1", o.ID).Scan(&n); err != nil || n != 0 {
			t.Fatalf("late admission persisted: %d %v", n, err)
		}
	})
	t.Run("concurrent identical confirmations all succeed and write once", func(t *testing.T) {
		proofs, first, d, o, courier, row := prepare(t)
		started := make(chan struct{}, 2)
		release := make(chan struct{})
		d.onInspect = func() { started <- struct{}{}; <-release }
		d2 := &proofDependencies{repo: &proofMemory{}, now: d.now, body: d.body, onInspect: func() { started <- struct{}{}; <-release }}
		second := NewProofReceipts(NewProofs(f.repo, d2, d2, d2, ProofOptions{UploadScope: proofs.uploadScope}), f.repo)
		input := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i, service := range []*ProofReceipts{first, second} {
			wg.Add(1)
			go func() { defer wg.Done(); _, errs[i] = service.ConfirmDelivery(f.ctx, courier, input) }()
		}
		for range 2 {
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				close(release)
				wg.Wait()
				t.Fatalf("confirmation never reached validation: %v", errs)
			}
		}
		close(release)
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var n int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='deliver'", o.ID).Scan(&n); err != nil || n != 1 {
			t.Fatalf("deliveries=%d %v", n, err)
		}
	})

}
