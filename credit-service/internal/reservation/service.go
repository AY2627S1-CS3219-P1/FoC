package reservation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type Service struct {
	repo   Repository
	clock  func() time.Time
	payout PayoutRule
}

func NewService(repo Repository, clock func() time.Time, payout PayoutRule) *Service {
	return &Service{repo: repo, clock: clock, payout: payout}
}

// GetReservation returns the current reservation, including tombstones.
func (s *Service) GetReservation(ctx context.Context, orderID string) (Reservation, error) {
	orderID, err := canonicalID(orderID)
	if err != nil {
		return Reservation{}, err
	}
	var out Reservation
	err = s.repo.Within(ctx, func(tx Tx) error {
		r, err := tx.Reservation(ctx, orderID)
		if err != nil {
			return err
		}
		if r == nil {
			return ErrReservationNotFound
		}
		out = *r
		return withAdjustment(ctx, tx, &out)
	})
	return out, err
}

// result is what a mutation produced: a reservation revision, or an
// INSUFFICIENT_CREDITS rejection that must still be committed.
type result struct {
	reservation  Reservation
	insufficient bool
}

// mutate runs apply at most once per operation key. A repeat with the same
// payload replays the first outcome; a different payload is a conflict.
// Refusals other than INSUFFICIENT_CREDITS store nothing, so a repeat
// recomputes them from current state.
func (s *Service) mutate(ctx context.Context, key OperationKey, payload any, apply func(Tx, time.Time) (result, error)) (Reservation, error) {
	hash, err := requestHash(payload)
	if err != nil {
		return Reservation{}, err
	}

	var out result
	err = s.repo.Within(ctx, func(tx Tx) error {
		if err := tx.LockOrder(ctx, key.OrderID); err != nil {
			return err
		}
		op, err := tx.Operation(ctx, key)
		if err != nil {
			return err
		}

		// Replay the first outcome of a repeated call.
		if op != nil {
			if op.RequestHash != hash {
				return ErrIdempotencyConflict
			}
			if op.Insufficient {
				out = result{insufficient: true}
				return nil
			}
			r, err := tx.Revision(ctx, key.OrderID, op.Revision)
			if err != nil {
				return err
			}
			out = result{reservation: r}
			return withAdjustment(ctx, tx, &out.reservation)
		}

		// First call: apply the mutation and record its outcome.
		now := s.clock().UTC()
		if out, err = apply(tx, now); err != nil {
			return err
		}

		record := Operation{
			RequestHash:  hash,
			Insufficient: out.insufficient,
			Revision:     out.reservation.Revision,
			CreatedAt:    now,
		}
		if err := tx.SaveOperation(ctx, key, record); err != nil {
			return err
		}
		if out.insufficient {
			return nil
		}
		return withAdjustment(ctx, tx, &out.reservation)
	})

	if err != nil {
		return Reservation{}, err
	}
	if out.insufficient {
		return Reservation{}, ErrInsufficientCredits
	}
	return out.reservation, nil
}

// withAdjustment describes the applied offer of an Assigned reservation. The
// offer is read after the reservation, so it reflects the locked state.
func withAdjustment(ctx context.Context, tx Tx, r *Reservation) error {
	r.Adjustment = nil
	if r.Status != Assigned {
		return nil
	}
	a, err := tx.Assignment(ctx, r.OrderID, r.AssignmentID)
	if err != nil {
		return err
	}
	if a == nil {
		return fmt.Errorf("assigned reservation %s has no assignment %s", r.OrderID, r.AssignmentID)
	}
	r.Adjustment = &Adjustment{
		AssignmentID:   a.AssignmentID,
		CourierID:      a.CourierID,
		PreviousAmount: r.OriginalAmount,
		OfferAmount:    a.OfferAmount,
		Status:         Applied,
	}
	return nil
}

// update saves next as the revision after current and returns it as stored.
func update(ctx context.Context, tx Tx, current Reservation, next Reservation) (Reservation, error) {
	return tx.UpdateReservation(ctx, next, current.Revision)
}

func requestHash(payload any) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("hash request: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalID(v string) (string, error) {
	id, err := uuid.Parse(v)
	if err != nil {
		return "", ErrInvalidArgument
	}
	return id.String(), nil
}

func canonicalIDs(vs ...*string) error {
	for _, v := range vs {
		id, err := canonicalID(*v)
		if err != nil {
			return err
		}
		*v = id
	}
	return nil
}
