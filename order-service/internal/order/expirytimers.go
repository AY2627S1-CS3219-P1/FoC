package order

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
)

const expiryTimerBatchSize = lifecycleTimerBatchSize

// ExpiryTimerTx shares the parent transaction and authoritative database clock.
type ExpiryTimerTx interface {
	Tx
	ProofServerTime(context.Context) (time.Time, error)
}

// ExpiryTimerRepository supplies advisory candidates and a locked transaction.
// Eligibility is rechecked under the parent lock, not taken from the scan.
type ExpiryTimerRepository interface {
	WithinExpiryTimers(context.Context, func(ExpiryTimerTx) error) error
	DueExpiryTimers(context.Context, time.Time, string, int32) ([]string, error)
}

type ExpiryTimerOptions struct {
	// TestNow overrides discovery and locked DB time only for deterministic tests.
	TestNow func() time.Time
	NewID   func() string
}

type ExpiryTimers struct {
	*lifecycleTimerScanner
	repository ExpiryTimerRepository
	testNow    func() time.Time
	newID      func() string
}

func NewExpiryTimers(repository ExpiryTimerRepository, options ExpiryTimerOptions) *ExpiryTimers {
	if options.NewID == nil {
		options.NewID = uuid.NewString
	}

	s := &ExpiryTimers{
		repository: repository,
		testNow:    options.TestNow,
		newID:      options.NewID,
	}
	s.lifecycleTimerScanner = newLifecycleTimerScanner("expiry", options.TestNow, repository.DueExpiryTimers, s.Expire)
	return s
}

// Expire preserves the requester's original deadline and financial observations.
// Assignment recovery, not this local transaction, compensates late Credit work.
func (s *ExpiryTimers) Expire(ctx context.Context, id string) error {
	err := s.repository.WithinExpiryTimers(ctx, func(tx ExpiryTimerTx) error {
		return NewMutator(
			borrowedTimerRepository{tx: tx},
		).Mutate(ctx, id, func(before Order, offers []Offer) (*Change, error) {
			if before.Status != AwaitingCourier {
				return nil, nil
			}

			now, err := lockedLifecycleTime(ctx, tx, s.testNow)
			if err != nil {
				return nil, err
			}
			if before.ExpiresAt.After(now) {
				return nil, nil
			}

			actor := Actor{Kind: System}
			next, _, err := Transition(before, Expire, actor)
			if err != nil {
				return nil, err
			}

			after := cloneOrder(before)
			reason := "request_expired"
			after.Status = next
			after.ClosedAt, after.CloseReason = &now, &reason
			after.UpdatedAt = now
			after.Revision++
			details, err := json.Marshal(struct {
				Deadline time.Time `json:"deadline"`
			}{Deadline: before.ExpiresAt})
			if err != nil {
				return nil, err
			}

			change, err := NewChange(&before, after, actor, Expire, details, s.newID)
			if err != nil {
				return nil, err
			}

			for _, offer := range offers {
				if offer.Status != OfferOpen && offer.Status != OfferAccepting {
					continue
				}
				expected := offer.Status
				offer.Status, offer.UpdatedAt = OfferVoid, now
				change.Offers = append(change.Offers, OfferChange{Offer: offer, ExpectedStatus: expected})
			}
			return &change, nil
		})
	})
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}
