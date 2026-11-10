package order

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ReceiptWindow is how long a delivered order waits for the requester to
// confirm receipt before it completes on its own. A requester with a problem
// confirms or lets it complete, then reports it separately.
const ReceiptWindow = time.Hour

const receiptTimerBatchSize = lifecycleTimerBatchSize

// ReceiptTimerTx shares the parent transaction and authoritative database clock.
type ReceiptTimerTx interface {
	Tx
	ProofServerTime(context.Context) (time.Time, error)
}

// ReceiptTimerRepository supplies advisory candidates and a locked transaction.
// Eligibility is rechecked under the parent lock, not taken from the scan.
type ReceiptTimerRepository interface {
	WithinReceiptTimers(context.Context, func(ReceiptTimerTx) error) error
	DueReceiptTimers(context.Context, time.Time, string, int32) ([]string, error)
}

type ReceiptTimerOptions struct {
	// TestNow overrides discovery and locked DB time only for deterministic tests.
	TestNow func() time.Time
	NewID   func() string
}

type ReceiptTimers struct {
	*lifecycleTimerScanner
	repository ReceiptTimerRepository
	testNow    func() time.Time
	newID      func() string
}

func NewReceiptTimers(repository ReceiptTimerRepository, options ReceiptTimerOptions) *ReceiptTimers {
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}

	s := &ReceiptTimers{
		repository: repository,
		testNow:    options.TestNow,
		newID:      options.NewID,
	}
	s.lifecycleTimerScanner = newLifecycleTimerScanner(
		"receipt",
		options.TestNow,
		repository.DueReceiptTimers,
		s.Complete,
	)
	return s
}

// Complete finishes a delivered order once ReceiptWindow has passed without a
// confirmation. A requester's confirmation or another instance wins by
// changing the same locked parent before this recheck.
func (s *ReceiptTimers) Complete(ctx context.Context, id string) error {
	err := s.repository.WithinReceiptTimers(ctx, func(tx ReceiptTimerTx) error {
		return NewMutator(
			borrowedTimerRepository{tx: tx},
		).Mutate(ctx, id, func(before Order, _ []Offer) (*Change, error) {
			if before.Status != Delivered || before.DeliveredAt == nil {
				return nil, nil
			}

			now, err := lockedLifecycleTime(ctx, tx, s.testNow)
			if err != nil {
				return nil, err
			}
			deadline := before.DeliveredAt.Add(ReceiptWindow)
			if now.Before(deadline) {
				return nil, nil
			}

			actor := Actor{Kind: System}
			next, _, err := Transition(before, AutoComplete, actor)
			if err != nil {
				return nil, err
			}

			after := cloneOrder(before)
			after.Status, after.UpdatedAt = next, now
			after.CompletedAt = &now
			after.AutomaticallyCompleted = true
			after.Revision++
			details, err := json.Marshal(struct {
				Deadline time.Time `json:"deadline"`
			}{Deadline: deadline})
			if err != nil {
				return nil, err
			}

			change, err := NewChange(&before, after, actor, AutoComplete, details, s.newID)
			if err != nil {
				return nil, err
			}
			return &change, nil
		})
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
