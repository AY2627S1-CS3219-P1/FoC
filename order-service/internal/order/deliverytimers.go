package order

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const deliveryTimerBatchSize = lifecycleTimerBatchSize

// DeliveryTimerTx borrows the proof transaction. The parent must be locked
// before checking pending submissions in a separate, fresh statement.
type DeliveryTimerTx interface {
	Tx
	HasPendingDelivery(context.Context, string) (bool, error)
	ProofServerTime(context.Context) (time.Time, error)
}

type DeliveryTimerRepository interface {
	WithinDeliveryTimers(context.Context, func(DeliveryTimerTx) error) error
	DueDeliveryTimers(context.Context, time.Time, string, int32) ([]string, error)
}

type DeliveryTimerOptions struct {
	// TestNow overrides discovery and locked DB time only for deterministic tests.
	TestNow func() time.Time
	NewID   func() string
}

type DeliveryTimers struct {
	*lifecycleTimerScanner
	repository DeliveryTimerRepository
	testNow    func() time.Time
	newID      func() string
}

func NewDeliveryTimers(repository DeliveryTimerRepository, options DeliveryTimerOptions) *DeliveryTimers {
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}

	s := &DeliveryTimers{
		repository: repository,
		testNow:    options.TestNow,
		newID:      options.NewID,
	}
	s.lifecycleTimerScanner = newLifecycleTimerScanner("delivery_timeout", options.TestNow, repository.DueDeliveryTimers, s.Timeout)
	return s
}

// Timeout never contacts storage or Credit. An on-time durable confirmation is
// protected while its owner validates/retries it. Cancellation and completed
// confirmations win by changing the same locked parent before this recheck.
func (s *DeliveryTimers) Timeout(ctx context.Context, id string) error {
	err := s.repository.WithinDeliveryTimers(ctx, func(tx DeliveryTimerTx) error {
		return NewMutator(borrowedTimerRepository{tx: tx}).Mutate(ctx, id, func(before Order, _ []Offer) (*Change, error) {
			if before.Status != Delivering {
				return nil, nil
			}

			deadline, err := deliveryTimeoutDeadline(before)
			if err != nil {
				return nil, err
			}
			now, err := lockedLifecycleTime(ctx, tx, s.testNow)
			if err != nil {
				return nil, err
			}
			if now.Before(deadline) {
				return nil, nil
			}

			pending, err := tx.HasPendingDelivery(ctx, id)
			if err != nil {
				return nil, err
			}
			if pending {
				return nil, nil
			}

			actor := Actor{Kind: System}
			next, _, err := Transition(before, Timeout, actor)
			if err != nil {
				return nil, err
			}

			after := cloneOrder(before)
			reason := "delivery_timed_out"
			after.Status, after.UpdatedAt = next, now
			after.ClosedAt, after.CloseReason = &now, &reason
			after.Revision++
			details, err := json.Marshal(struct {
				Deadline time.Time `json:"deadline"`
			}{Deadline: deadline})
			if err != nil {
				return nil, err
			}

			change, err := NewChange(&before, after, actor, Timeout, details, s.newID)
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

func deliveryTimeoutDeadline(o Order) (time.Time, error) {
	// Validate before multiplying a persisted duration to avoid overflow on a
	// malformed/legacy row. Normal creation limits are much smaller.
	if o.PickedUpAt == nil || o.CourierID == nil || o.DeliveryDuration <= 0 || o.DeliveryDuration > time.Duration(1<<63-1)/2 {
		return time.Time{}, ErrPrecondition
	}
	return o.PickedUpAt.Add(2 * o.DeliveryDuration), nil
}
