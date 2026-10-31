package order

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

const assignmentLease = 15 * time.Second

type assignmentObservation struct {
	reservation *AssignmentReservation
	refused     bool
}

// ProcessAssignment claims one financial step. The lease/generation fences late
// responses, while Credit's permanent attempt identity fences delayed RPCs.
func (s *Assigner) ProcessAssignment(ctx context.Context, attemptID string) error {
	if s.credit == nil {
		return ErrUnavailable
	}
	orderID, err := s.repository.AssignmentOrderID(ctx, attemptID)
	if err != nil {
		return err
	}
	var claimed *AssignmentAttempt
	err = s.mutate(ctx, orderID, nil, func(tx AssignmentTx, o Order, _ []Offer) (*Change, error) {
		w, err := tx.FindAssignment(ctx, attemptID)
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		if w.IsResolved() || w.LeaseUntil.After(now) ||
			(w.NextAttemptAt.After(now) && (w.Phase != AdjustmentPending || (o.Status == AwaitingCourier && o.ExpiresAt.After(now)))) {
			return nil, nil
		}
		active, err := tx.ActiveAssignment(ctx, orderID)
		if err != nil {
			return nil, err
		}
		if active == nil || active.ID != w.ID {
			return nil, ErrConflict
		}
		if o.Status != AwaitingCourier || !o.ExpiresAt.After(now) {
			w.Phase = RevertPending
			w.Rejection = "order_unavailable"
		}
		expected := w.Generation
		w.Generation++
		w.Attempts++
		w.LeaseUntil, w.UpdatedAt = now.Add(assignmentLease), now
		if err := tx.SaveAssignment(ctx, w, expected); err != nil {
			return nil, err
		}
		claimed = &w
		return nil, nil
	})
	if err != nil || claimed == nil {
		return err
	}

	observation := s.observeAssignment(ctx, *claimed)
	revertReady := false
	err = s.mutate(ctx, orderID, nil, func(tx AssignmentTx, o Order, offers []Offer) (*Change, error) {
		w, err := tx.FindAssignment(ctx, attemptID)
		if err != nil {
			return nil, err
		}
		now := s.now().UTC()
		if w.IsResolved() || w.Generation != claimed.Generation || w.Phase != claimed.Phase ||
			!w.LeaseUntil.After(now) {
			return nil, nil
		}
		var change *Change
		switch {
		case w.Phase == RevertPending:
			if confirmsRevert(observation.reservation, w, o) {
				w.Phase = AssignmentRejected
				status := OfferVoid
				if w.Method != AcceptOrderMethod && w.Rejection == "insufficient_credits" &&
					o.Status == AwaitingCourier &&
					o.ExpiresAt.After(now) {
					status = OfferOpen
				}
				change, err = s.resolveOffer(o, offers, w, status, now)
			}
		case o.Status != AwaitingCourier || !o.ExpiresAt.After(now):
			w.Phase, w.Rejection = RevertPending, "order_unavailable"
			revertReady = true
		case observation.refused:
			// Another generation may still have a delayed Adjust in flight.
			// Fence this key even after refusal; do not assume Credit retains
			// insufficient-funds errors forever when the balance later changes.
			w.Phase, w.Rejection = RevertPending, "insufficient_credits"
			revertReady = true
		case confirmsAdjustment(observation.reservation, w, o):
			change, err = s.assignChange(o, offers, w, now)
			if err == nil {
				w.Phase = AssignmentSucceeded
			}
		}
		if err != nil {
			return nil, err
		}
		w.LeaseUntil, w.UpdatedAt = now, now
		w.NextAttemptAt = now.Add(creationRetryDelay(w.Attempts))
		if w.Phase == AdjustmentPending && o.ExpiresAt.Before(w.NextAttemptAt) {
			w.NextAttemptAt = o.ExpiresAt
		}
		if revertReady {
			w.NextAttemptAt = now
		}
		if err := tx.SaveAssignment(ctx, w, w.Generation); err != nil {
			return nil, err
		}
		return change, nil
	})
	if err != nil {
		return err
	}
	if revertReady {
		return s.ProcessAssignment(ctx, attemptID)
	}
	return nil
}

func (s *Assigner) observeAssignment(ctx context.Context, w AssignmentAttempt) assignmentObservation {
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	var err error
	if w.Phase == RevertPending {
		err = s.credit.Revert(call, w.OrderID, w.ID)
	} else {
		err = s.credit.Adjust(
			call,
			AdjustmentIntent{OrderID: w.OrderID, OfferID: w.ID, CourierID: w.CourierID, Amount: w.Amount},
		)
	}
	cancel()
	if w.Phase == AdjustmentPending && errors.Is(err, ErrCreditInsufficient) {
		return assignmentObservation{refused: true}
	}
	// A revert must be acknowledged, even if a read currently says Held: the
	// acknowledgement proves that a delayed adjustment has been fenced.
	if w.Phase == RevertPending && err != nil {
		return assignmentObservation{}
	}
	call, cancel = context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	view, readErr := s.credit.AssignmentReservation(call, w.OrderID)
	if readErr != nil {
		return assignmentObservation{}
	}
	return assignmentObservation{reservation: &view}
}

func confirmsAdjustment(v *AssignmentReservation, w AssignmentAttempt, o Order) bool {
	return v != nil && v.OrderID == w.OrderID && v.RequesterID == o.RequesterID && v.State == "assigned" &&
		v.OfferID == w.ID && v.CourierID == w.CourierID && v.ReservedAmount == w.Amount && v.OriginalAmount == w.OriginalAmount
}

func confirmsRevert(v *AssignmentReservation, w AssignmentAttempt, o Order) bool {
	if v == nil || v.OrderID != w.OrderID || v.RequesterID != o.RequesterID || v.OriginalAmount != w.OriginalAmount ||
		v.OfferID != "" ||
		v.CourierID != "" {
		return false
	}
	return (v.State == "held" && v.ReservedAmount == w.OriginalAmount && v.OriginalAmount == w.OriginalAmount) ||
		(v.State == "released" && v.ReservedAmount == 0)
}

func (s *Assigner) resolveOffer(
	o Order,
	offers []Offer,
	w AssignmentAttempt,
	status OfferStatus,
	now time.Time,
) (*Change, error) {
	change, err := s.metadata(o, Caller{ID: w.CallerID}, ResolveAssignment, now)
	if err != nil {
		return nil, err
	}
	for _, f := range offers {
		if f.ID == w.OfferID {
			if f.Status != OfferAccepting && f.Status != OfferVoid {
				return nil, ErrConflict
			}
			before := f.Status
			f.Status, f.UpdatedAt = status, now
			// Never reactivate an offer another lifecycle action has voided.
			if before == OfferVoid {
				f.Status = OfferVoid
			}
			change.Offers = []OfferChange{{Offer: f, ExpectedStatus: before}}
			return change, nil
		}
	}
	return nil, ErrIdempotencyIntegrity
}

func (s *Assigner) assignChange(o Order, offers []Offer, w AssignmentAttempt, now time.Time) (*Change, error) {
	if w.CourierID == o.RequesterID {
		return nil, ErrForbidden
	}
	action := Assign
	if w.Method != AcceptOrderMethod {
		action = AcceptCounteroffer
	}
	actor := Actor{Kind: User, ID: w.CallerID}
	next, repeat, err := Transition(o, action, actor)
	if err != nil {
		return nil, err
	}
	if repeat {
		return nil, ErrConflict
	}
	after := cloneOrder(o)
	after.Status, after.CourierID = next, &w.CourierID
	after.RewardCredits, after.ReservedCredits = w.Amount, w.Amount
	after.AssignedAt, after.UpdatedAt = &now, now
	after.Revision++
	change, err := NewChange(&o, after, actor, action, nil, s.newID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, f := range offers {
		switch {
		case f.ID == w.OfferID:
			if f.Status != OfferAccepting || f.CourierID != w.CourierID || f.Credits != w.Amount {
				return nil, ErrConflict
			}
			f.Status, f.UpdatedAt = OfferAccepted, now
			change.Offers = append(change.Offers, OfferChange{Offer: f, ExpectedStatus: OfferAccepting})
			found = true
		case f.Status == OfferOpen:
			f.Status, f.UpdatedAt = OfferVoid, now
			change.Offers = append(change.Offers, OfferChange{Offer: f, ExpectedStatus: OfferOpen})
		}
	}
	if !found {
		return nil, ErrIdempotencyIntegrity
	}
	return &change, nil
}

// RecoverAssignments resumes persisted intent on startup and after each scan.
// Each attempt has a durable lease so multiple instances can recover safely.
func (s *Assigner) RecoverAssignments(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := s.RecoverAssignmentsOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Assignment recovery scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Assigner) RecoverAssignmentsOnce(ctx context.Context) error {
	ids, err := s.repository.DueAssignments(ctx, s.now().UTC(), 50)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := s.ProcessAssignment(attempt, id)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Assignment recovery deferred", "attempt_id", id)
		}
	}
	return nil
}
