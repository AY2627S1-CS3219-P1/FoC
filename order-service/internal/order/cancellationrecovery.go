package order

import (
	"context"
	"log/slog"
	"time"
)

// ProcessCancellation leases one reversal. A missing durable adjustment identity
// remains pending: a current Held observation alone cannot fence a delayed Adjust.
func (s *Canceller) ProcessCancellation(ctx context.Context, operationID string) error {
	if s.credit == nil {
		return ErrUnavailable
	}
	orderID, err := s.repository.CancellationOrderID(ctx, operationID)
	if err != nil {
		return err
	}
	var claimed *Cancellation
	err = s.mutate(ctx, orderID, nil, func(tx CancellationTx, o Order, _ []Offer) (*Change, error) {
		w, err := tx.FindCancellation(ctx, operationID)
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		if w.Status != CancellationPending || w.LeaseUntil.After(now) || w.NextAttemptAt.After(now) {
			return nil, nil
		}
		if !matchesWithdrawal(o, w) {
			return nil, ErrConflict
		}
		expected := w.Generation
		w.Generation++
		w.Attempts++
		w.LeaseUntil, w.UpdatedAt = now.Add(assignmentLease), now
		if err := tx.SaveCancellation(ctx, w, expected); err != nil {
			return nil, err
		}
		claimed = &w
		return nil, nil
	})
	if err != nil || claimed == nil {
		return err
	}
	var view *AssignmentReservation
	if claimed.AssignmentID != "" && claimed.OriginalAmount > 0 {
		call, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := s.credit.Revert(call, claimed.OrderID, claimed.AssignmentID)
		cancel()
		if err == nil {
			call, cancel = context.WithTimeout(ctx, 2*time.Second)
			observed, readErr := s.credit.AssignmentReservation(call, claimed.OrderID)
			cancel()
			if readErr == nil {
				view = &observed
			}
		}
	}
	return s.mutate(ctx, orderID, nil, func(tx CancellationTx, o Order, _ []Offer) (*Change, error) {
		w, err := tx.FindCancellation(ctx, operationID)
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		if w.Status != CancellationPending || w.Generation != claimed.Generation || !w.LeaseUntil.After(now) {
			return nil, nil
		}
		if !matchesWithdrawal(o, w) {
			return nil, ErrConflict
		}
		var change *Change
		if confirmsWithdrawal(view, w, o) {
			after := cloneOrder(o)
			after.Status, after.CourierID, after.AssignedAt, after.PendingCancellationID = AwaitingCourier, nil, nil, nil
			after.RewardCredits, after.ReservedCredits = w.OriginalAmount, w.OriginalAmount
			after.Revision++
			after.UpdatedAt = now
			c, err := NewChange(&o, after, Actor{Kind: User, ID: w.CallerID}, Withdraw, nil, s.newID)
			if err != nil {
				return nil, err
			}
			change = &c
			w.Status = CancellationSucceeded
		}
		w.LeaseUntil, w.UpdatedAt = now, now
		w.NextAttemptAt = now.Add(creationRetryDelay(w.Attempts))
		if err := tx.SaveCancellation(ctx, w, w.Generation); err != nil {
			return nil, err
		}
		return change, nil
	})
}

func matchesWithdrawal(o Order, w Cancellation) bool {
	return o.Status == AwaitingPickup && o.PendingCancellationID != nil && *o.PendingCancellationID == w.ID &&
		o.IsAssignedTo(w.CourierID) && o.AssignedAt != nil && w.AssignedAt != nil && o.AssignedAt.Equal(*w.AssignedAt) && o.PickedUpAt == nil
}

func confirmsWithdrawal(v *AssignmentReservation, w Cancellation, o Order) bool {
	return v != nil && w.AssignmentID != "" && w.OriginalAmount > 0 && v.OrderID == w.OrderID &&
		v.RequesterID == o.RequesterID && v.State == "held" && v.OriginalAmount == w.OriginalAmount &&
		v.ReservedAmount == w.OriginalAmount && v.OfferID == "" && v.CourierID == ""
}

func (s *Canceller) RecoverCancellations(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.RecoverCancellationsOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Cancellation recovery scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Canceller) RecoverCancellationsOnce(ctx context.Context) error {
	ids, err := s.repository.DueCancellations(ctx, s.now().UTC(), 50)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.ProcessCancellation(call, id)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Cancellation recovery deferred", "operation_id", id)
		}
	}
	return nil
}
