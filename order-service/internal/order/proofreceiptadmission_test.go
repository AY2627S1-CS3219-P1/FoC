package order

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// Only a missing proof is an authoritative rejection. A failed admission read
// must leave the caller able to retry the same confirmation after recovery.
func TestProofReceiptAdmissionPreservesUploadReadFailures(t *testing.T) {
	for _, readErr := range []error{errors.New("database connection lost"), context.DeadlineExceeded, fmt.Errorf("missing upload: %w", ErrNotFound)} {
		t.Run(readErr.Error(), func(t *testing.T) {
			proofs, memory, dependencies, now := proofScenario()
			row := uploadedProof(t, proofs, dependencies)
			repository := &admissionReadRepository{base: memory, readErr: readErr, now: *now}
			proofs.repository = repository
			receipts := NewProofReceipts(proofs, repository)
			_, err := receipts.ConfirmDelivery(t.Context(), Caller{ID: proofCourier}, proofConfirmationInput(row))
			want := readErr
			if errors.Is(readErr, ErrNotFound) {
				want = ErrPrecondition
			}
			if !errors.Is(err, want) {
				t.Fatalf("admission returned %v, want %v", err, want)
			}
			if memory.order.Status != Delivering || memory.confirmation != nil || len(memory.changes) != 0 || repository.inserted {
				t.Fatal("failed admission changed delivery or created a receipt")
			}
		})
	}
}

type admissionReadRepository struct {
	ReceiptRepository
	base     ProofRepository
	readErr  error
	now      time.Time
	inserted bool
}

func (r *admissionReadRepository) WithinProofs(ctx context.Context, fn func(ProofTx) error) error {
	return r.base.WithinProofs(ctx, func(tx ProofTx) error {
		return fn(&admissionReadTx{ProofTx: tx, repository: r})
	})
}

type admissionReadTx struct {
	ProofTx
	repository *admissionReadRepository
}

func (t *admissionReadTx) GetProofUpload(context.Context, string) (ProofUpload, error) {
	return ProofUpload{}, t.repository.readErr
}

func (t *admissionReadTx) ProofServerTime(context.Context) (time.Time, error) {
	return t.repository.now, nil
}

func (*admissionReadTx) GetPendingDeliveryReceipt(context.Context, string) (DeliveryReceipt, error) {
	return DeliveryReceipt{}, ErrNotFound
}

func (*admissionReadTx) GetDeliveryReceipt(context.Context, string) (DeliveryReceipt, error) {
	return DeliveryReceipt{}, ErrNotFound
}

func (t *admissionReadTx) InsertDeliveryReceipt(context.Context, DeliveryReceipt) error {
	t.repository.inserted = true
	return nil
}

func (*admissionReadTx) FinishDeliveryReceipt(context.Context, string, string, time.Time) error {
	return nil
}
