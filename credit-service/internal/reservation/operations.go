package reservation

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
)

// Reserve holds amount from the requester's available credits (C1.1.1). A
// requester without an account first receives the starting credits. An order
// released before Credit saw it cannot be reserved.
func (s *Service) Reserve(ctx context.Context, orderID, requesterID string, amount int64) (Reservation, error) {
	if err := canonicalIDs(&orderID, &requesterID); err != nil {
		return Reservation{}, err
	}
	if amount <= 0 {
		return Reservation{}, ErrInvalidArgument
	}

	key := OperationKey{Kind: OpReserve, OrderID: orderID}
	payload := struct {
		RequesterID string
		Amount      int64
	}{requesterID, amount}

	return s.mutate(ctx, key, payload, func(tx Tx, now time.Time) (result, error) {
		r, err := tx.Reservation(ctx, orderID)
		if err != nil {
			return result{}, err
		}
		if r != nil {
			// Reserve records every reservation it creates, so this is a
			// tombstone or an order reserved under another key.
			if r.Status == Released {
				return result{}, ErrReservationReleased
			}
			return result{}, ErrIdempotencyConflict
		}

		accounts, err := tx.LockAccounts(ctx, now, requesterID)
		if err != nil {
			return result{}, err
		}
		if amount > accounts[requesterID].Available() {
			return result{insufficient: true}, nil
		}

		next := Reservation{
			OrderID:        orderID,
			RequesterID:    requesterID,
			Status:         Held,
			OriginalAmount: amount,
			ReservedAmount: amount,
			Revision:       1,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if next, err = tx.InsertReservation(ctx, next); err != nil {
			return result{}, err
		}

		err = tx.Post(ctx, now, Entry{
			UserID:        requesterID,
			Kind:          account.Reserve,
			ReservedDelta: amount,
			OrderID:       orderID,
		})
		return result{reservation: next}, err
	})
}

// AdjustReservation applies an accepted offer to a Held reservation and
// records the courier. The hold becomes max(original, offer): credits an
// offer below the original would free stay held until settlement, so
// RevertAdjustment never needs more credits than the requester holds.
func (s *Service) AdjustReservation(ctx context.Context, orderID, assignmentID, courierID string, offerAmount int64) (Reservation, error) {
	if err := canonicalIDs(&orderID, &assignmentID, &courierID); err != nil {
		return Reservation{}, err
	}
	if offerAmount <= 0 {
		return Reservation{}, ErrInvalidArgument
	}

	key := OperationKey{Kind: OpAdjust, OrderID: orderID, AssignmentID: assignmentID}
	payload := struct {
		CourierID   string
		OfferAmount int64
	}{courierID, offerAmount}

	return s.mutate(ctx, key, payload, func(tx Tx, now time.Time) (result, error) {
		// Validate the reservation and the offer.
		r, err := tx.Reservation(ctx, orderID)
		if err != nil {
			return result{}, err
		}
		if err := requireOpen(r); err != nil {
			return result{}, err
		}

		a, err := tx.Assignment(ctx, orderID, assignmentID)
		if err != nil {
			return result{}, err
		}
		if a != nil && a.Status == Reverted {
			return result{}, ErrAdjustmentReverted
		}
		if a != nil || r.Status == Assigned {
			return result{}, ErrAdjustmentPending
		}

		// Check that the requester can cover any increase in the hold.
		hold := max(r.OriginalAmount, offerAmount)
		delta := hold - r.ReservedAmount
		accounts, err := tx.LockAccounts(ctx, now, r.RequesterID)
		if err != nil {
			return result{}, err
		}
		if delta > accounts[r.RequesterID].Available() {
			return result{insufficient: true}, nil
		}

		// Apply the offer.
		if err := tx.InsertAssignment(ctx, Assignment{
			OrderID:      orderID,
			AssignmentID: assignmentID,
			CourierID:    courierID,
			OfferAmount:  offerAmount,
			Status:       Applied,
			CreatedAt:    now,
		}); err != nil {
			return result{}, err
		}

		next := *r
		next.Status = Assigned
		next.ReservedAmount = hold
		next.AssignmentID = assignmentID
		if next, err = update(ctx, tx, *r, next); err != nil {
			return result{}, err
		}

		err = tx.Post(ctx, now, Entry{
			UserID:        r.RequesterID,
			Kind:          account.Adjust,
			ReservedDelta: delta,
			OrderID:       orderID,
			AssignmentID:  assignmentID,
		})
		return result{reservation: next}, err
	})
}

// RevertAdjustment undoes an applied offer after Order loses the assignment
// race: the hold returns to the original amount and the reservation to Held.
// An unknown offer is recorded as reverted so a later AdjustReservation for it
// fails. A Released reservation has nothing to undo.
func (s *Service) RevertAdjustment(ctx context.Context, orderID, assignmentID string) (Reservation, error) {
	if err := canonicalIDs(&orderID, &assignmentID); err != nil {
		return Reservation{}, err
	}

	key := OperationKey{Kind: OpRevert, OrderID: orderID, AssignmentID: assignmentID}

	return s.mutate(ctx, key, struct{}{}, func(tx Tx, now time.Time) (result, error) {
		r, err := tx.Reservation(ctx, orderID)
		if err != nil {
			return result{}, err
		}
		if r == nil {
			return result{}, ErrReservationNotFound
		}
		if r.Status == Settled {
			return result{}, ErrReservationSettled
		}

		a, err := tx.Assignment(ctx, orderID, assignmentID)
		if err != nil {
			return result{}, err
		}
		switch {
		case a == nil:
			err = tx.InsertAssignment(ctx, Assignment{
				OrderID:      orderID,
				AssignmentID: assignmentID,
				Status:       Reverted,
				CreatedAt:    now,
				RevertedAt:   &now,
			})
			return result{reservation: *r}, err
		case a.Status == Reverted:
			return result{reservation: *r}, nil
		}

		if err := tx.RevertAssignment(ctx, orderID, assignmentID, now); err != nil {
			return result{}, err
		}
		if r.Status != Assigned || r.AssignmentID != assignmentID {
			// Released after the offer was applied: the hold is already back.
			return result{reservation: *r}, nil
		}

		next := *r
		next.Status = Held
		next.ReservedAmount = r.OriginalAmount
		next.AssignmentID = ""
		if next, err = update(ctx, tx, *r, next); err != nil {
			return result{}, err
		}

		err = tx.Post(ctx, now, Entry{
			UserID:        r.RequesterID,
			Kind:          account.Revert,
			ReservedDelta: r.OriginalAmount - r.ReservedAmount,
			OrderID:       orderID,
			AssignmentID:  assignmentID,
		})
		return result{reservation: next}, err
	})
}

// ReleaseReservation returns the whole hold to the requester. Order uses it
// to compensate a creation that never opened. An unknown order is recorded as
// released, so a delayed Reserve for it fails.
func (s *Service) ReleaseReservation(ctx context.Context, orderID string, reason ReleaseReason) (Reservation, error) {
	orderID, err := canonicalID(orderID)
	if err != nil {
		return Reservation{}, err
	}
	if !reason.valid() {
		return Reservation{}, ErrInvalidArgument
	}

	key := OperationKey{Kind: OpRelease, OrderID: orderID}
	payload := struct{ Reason ReleaseReason }{reason}

	return s.mutate(ctx, key, payload, func(tx Tx, now time.Time) (result, error) {
		r, err := tx.Reservation(ctx, orderID)
		if err != nil {
			return result{}, err
		}

		next, _, err := release(ctx, tx, orderID, r, reason, now)
		return result{reservation: next}, err
	})
}

// release returns r's hold to the requester, or records a tombstone when r is
// nil. It reports whether anything changed.
func release(ctx context.Context, tx Tx, orderID string, r *Reservation, reason ReleaseReason, now time.Time) (Reservation, bool, error) {
	if r == nil {
		tombstone := Reservation{
			OrderID: orderID,
			Status:  Released,
			Settlement: &Settlement{
				Outcome:       OutcomeReleased,
				SettledAt:     now,
				ReleaseReason: reason,
			},
			Revision:  1,
			CreatedAt: now,
			UpdatedAt: now,
		}
		stored, err := tx.InsertReservation(ctx, tombstone)
		return stored, true, err
	}

	switch r.Status {
	case Settled:
		return Reservation{}, false, ErrReservationSettled
	case Released:
		return *r, false, nil
	}

	next := *r
	next.Status = Released
	next.ReservedAmount = 0
	next.Settlement = &Settlement{
		Outcome:             OutcomeReleased,
		ReturnedToRequester: r.ReservedAmount,
		SettledAt:           now,
		ReleaseReason:       reason,
	}
	next, err := update(ctx, tx, *r, next)
	if err != nil {
		return Reservation{}, false, err
	}

	err = tx.Post(ctx, now, Entry{
		UserID:        r.RequesterID,
		Kind:          account.Release,
		ReservedDelta: -r.ReservedAmount,
		OrderID:       orderID,
	})
	return next, true, err
}

func requireOpen(r *Reservation) error {
	switch {
	case r == nil:
		return ErrReservationNotFound
	case r.Status == Released:
		return ErrReservationReleased
	case r.Status == Settled:
		return ErrReservationSettled
	}
	return nil
}
