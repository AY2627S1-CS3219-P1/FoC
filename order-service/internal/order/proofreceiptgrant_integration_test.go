//go:build integration

package order

import (
	"context"
	"github.com/google/uuid"
	"testing"
	"time"
)

// Source: a valid on-time immutable submission must remain eligible through
// processing delay. Signing metadata is not replacement proof evidence.
func TestProofGrantRefreshPreservesPendingReceipt(t *testing.T) {
	f := setup(t)
	o := f.create(t)
	caller := Caller{ID: uuid.NewString()}
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	f.mustTransition(t, o.ID, Assign, Actor{Kind: User, ID: caller.ID})
	if _, err := NewProgress(NewMutator(f.repo)).ConfirmPickup(f.ctx, caller, o.ID); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	dependencies := &proofDependencies{repo: &proofMemory{}, now: &now, body: []byte("verified immutable pixels")}
	issuer := &rotatingProofIssuer{proofDependencies: dependencies, epoch: "before-rotation"}
	proofs := NewProofs(f.repo, issuer, dependencies, dependencies, ProofOptions{UploadScope: "proof-primary"})
	input := ProofUploadInput{
		OrderID:     o.ID,
		UploadID:    uuid.NewString(),
		ContentType: "image/png",
		SizeBytes:   int64(len(dependencies.body)),
	}
	row, _, err := proofs.CreateUpload(f.ctx, caller, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = proofs.VerifyUpload(f.ctx, caller, o.ID, row.ID); err != nil {
		t.Fatal(err)
	}
	proofs.validator = &grantRefreshValidator{inner: dependencies, refresh: func() {
		issuer.epoch = "after-rotation"
		refreshed, _, err := proofs.CreateUpload(f.ctx, caller, input)
		if err != nil || refreshed.ID != row.ID || refreshed.Generation <= row.Generation {
			t.Fatalf("refresh setup: %+v %v", refreshed, err)
		}
	}}
	receipts := NewProofReceipts(proofs, f.repo)
	confirm := ConfirmDeliveryInput{OrderID: o.ID, ProofIDs: []string{row.ID}}
	_, _ = receipts.ConfirmDelivery(f.ctx, caller, confirm)
	var state string
	var received time.Time
	if err = f.pool.QueryRow(f.ctx, "SELECT state,received_at FROM order_delivery_receipts WHERE order_id=$1", o.ID).
		Scan(&state, &received); err != nil {
		t.Fatal(err)
	}
	if state != "pending" && state != "accepted" {
		t.Fatalf("grant rotation rejected valid immutable on-time evidence: receipt=%s", state)
	}
	accepted, err := receipts.ConfirmDelivery(f.ctx, caller, confirm)
	if err != nil || accepted.DeliveredAt == nil || !accepted.DeliveredAt.Equal(received) {
		t.Fatalf("receipt time lost after grant refresh: %+v %v", accepted, err)
	}
}

type grantRefreshValidator struct {
	inner   ProofImageValidator
	refresh func()
}

func (v *grantRefreshValidator) Validate(ctx context.Context, media string, body []byte) error {
	if err := v.inner.Validate(ctx, media, body); err != nil {
		return err
	}
	if v.refresh != nil {
		action := v.refresh
		v.refresh = nil
		action()
	}
	return nil
}
