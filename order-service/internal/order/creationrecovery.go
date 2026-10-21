package order

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"
)

const creationLease = 15 * time.Second

// ProcessCreation performs at most one leased financial step, plus an immediate
// release if expiry wins while Reserve is in flight. No remote call holds locks.
func (s *Creator) ProcessCreation(ctx context.Context, orderID string) error {
	if s.credit == nil {
		return ErrUnavailable
	}
	var claimed *CreationWorkflow
	err := s.repository.WithinCreation(ctx, func(tx CreationTx) error {
		o, err := tx.TryLockCreationOrder(ctx, orderID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		w, err := tx.LockCreation(ctx, orderID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if w.IsResolved() || w.LeaseUntil.After(now) || (w.NextAttemptAt.After(now) && (w.Phase != ReservePending || w.ExpiresAt.After(now))) {
			return nil
		}
		if o.Status != PendingReservation {
			return ErrConflict
		}
		expected := w.Generation
		if w.Phase == ReservePending && !w.ExpiresAt.After(now) {
			w.Phase = AbortPending
		}
		w.Generation++
		w.Attempts++
		w.LeaseUntil = now.Add(creationLease)
		w.UpdatedAt = now
		if err := tx.SaveCreation(ctx, w, expected); err != nil {
			return err
		}
		claimed = &w
		return nil
	})
	if err != nil || claimed == nil {
		return err
	}

	observation := s.observeCreation(ctx, *claimed)
	abortReady := false
	err = s.repository.WithinCreation(ctx, func(tx CreationTx) error {
		o, err := tx.LockOrder(ctx, orderID)
		if err != nil {
			return err
		}
		if _, err := tx.Relationships(ctx, orderID); err != nil {
			return err
		}
		w, err := tx.LockCreation(ctx, orderID)
		if err != nil {
			return err
		}
		now := s.now().UTC()
		if w.ID != claimed.ID || w.Generation != claimed.Generation || w.Phase != claimed.Phase || w.IsResolved() || !w.LeaseUntil.After(now) {
			return nil
		}
		if o.Status != PendingReservation {
			return ErrConflict
		}

		var rejection CreationRejection
		switch {
		case w.Phase == AbortPending:
			if observation.view != nil && isReleased(*observation.view, w.OrderID) {
				rejection = CreationExpired
			}
		case observation.refused:
			rejection = InsufficientCredits
		case !w.ExpiresAt.After(now):
			// Abort intent commits BEFORE any release. No subsequent Reserve response
			// can authorize opening once this workflow has entered AbortPending.
			w.Phase = AbortPending
			abortReady = true
		case observation.view != nil && isReleased(*observation.view, w.OrderID):
			rejection = ReservationReleased
		case observation.view != nil && isMatchingHold(*observation.view, w):
			after := cloneOrder(o)
			after.Status = AwaitingCourier
			after.ReservedCredits = w.Amount
			after.Revision++
			after.UpdatedAt = now
			change, err := NewChange(&o, after, Actor{Kind: System}, ConfirmReservation, nil, s.newID)
			if err != nil {
				return err
			}
			if err := tx.Apply(ctx, change); err != nil {
				return err
			}
			w.Phase = CreationOpened
			w.Result = &after
		}
		if rejection != "" {
			after := cloneOrder(o)
			after.Status = Rejected
			after.Revision++
			after.UpdatedAt, after.ClosedAt = now, &now
			reason := rejection.Error()
			after.CloseReason = &reason
			change, err := NewChange(&o, after, Actor{Kind: System}, RefuseReservation, nil, s.newID)
			if err != nil {
				return err
			}
			if err := tx.Apply(ctx, change); err != nil {
				return err
			}
			w.Phase, w.Rejection = CreationRejected, rejection
		}

		w.LeaseUntil, w.UpdatedAt = now, now
		w.LastError = observation.diagnostic
		if !w.IsResolved() && observation.view != nil && !isMatchingHold(*observation.view, w) && !isReleased(*observation.view, w.OrderID) {
			w.LastError = "reservation_mismatch"
		}
		if w.IsResolved() {
			w.LastError = ""
		}
		w.NextAttemptAt = now.Add(creationRetryDelay(w.Attempts))
		if w.Phase == ReservePending && w.ExpiresAt.Before(w.NextAttemptAt) {
			w.NextAttemptAt = w.ExpiresAt
		}
		if abortReady {
			w.NextAttemptAt = now
		}
		if err := tx.SaveCreation(ctx, w, w.Generation); err != nil {
			return err
		}
		slog.InfoContext(ctx, "Creation financial step", "order_id", w.OrderID, "workflow_id", w.ID, "phase", w.Phase, "attempt", w.Attempts, "diagnostic", w.LastError, "age_seconds", now.Sub(w.CreatedAt).Seconds())
		return nil
	})
	if err != nil {
		return err
	}
	if abortReady {
		return s.ProcessCreation(ctx, orderID)
	}
	return nil
}

type creationObservation struct {
	view       *ReservationView
	refused    bool
	diagnostic string
}

func (s *Creator) observeCreation(ctx context.Context, w CreationWorkflow) creationObservation {
	call, cancel := context.WithTimeout(ctx, 2*time.Second)
	var err error
	if w.Phase == AbortPending {
		err = s.credit.ReleaseExpired(call, w.OrderID)
	} else {
		err = s.credit.Reserve(call, w.Intent())
	}
	cancel()
	if w.Phase == ReservePending && errors.Is(err, ErrCreditInsufficient) {
		return creationObservation{refused: true}
	}
	// Mutation success can be historical; an unknown result can also already have
	// committed. In either case only this current-state read may confirm the hold.
	call, cancel = context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	view, getErr := s.credit.GetReservation(call, w.OrderID)
	if getErr != nil {
		return creationObservation{diagnostic: "reservation_state_unknown"}
	}
	if w.Phase == AbortPending && !isReleased(view, w.OrderID) {
		return creationObservation{diagnostic: "release_unconfirmed"}
	}
	return creationObservation{view: &view}
}

func isMatchingHold(v ReservationView, w CreationWorkflow) bool {
	return v.State == ReservationHeld && v.OrderID == w.OrderID && v.RequesterID == w.RequesterID && v.OriginalAmount == w.Amount && v.ReservedAmount == w.Amount
}

func isReleased(v ReservationView, id string) bool {
	return v.OrderID == id && v.State == ReservationIsReleased && v.ReservedAmount == 0
}

func creationRetryDelay(attempt int32) time.Duration {
	shift := max(int32(0), min(attempt-1, int32(5)))
	base := time.Second * time.Duration(1<<shift)
	return min(30*time.Second, base+time.Duration(rand.Float64()*float64(base)/4))
}

// RecoverCreations runs on service lifetime, independent of admission or user
// requests. Leases allow multiple Order instances and recovery after crashes.
func (s *Creator) RecoverCreations(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := s.RecoverCreationsOnce(ctx); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Creation recovery scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Creator) RecoverCreationsOnce(ctx context.Context) error {
	ids, err := s.repository.DueCreations(ctx, s.now().UTC(), 50)
	if err != nil {
		return err
	}
	jobs := make(chan string)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for id := range jobs {
				attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
				err := s.ProcessCreation(attempt, id)
				cancel()
				if err != nil && ctx.Err() == nil {
					slog.WarnContext(ctx, "Creation recovery attempt deferred", "order_id", id)
				}
			}
		})
	}
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return ctx.Err()
		}
	}
	close(jobs)
	workers.Wait()
	return nil
}
