package reservation

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
)

// ApplyOutcome settles or releases an order's reservation from Order's
// order.outcome event, the only terminal settlement trigger. Terminal
// reservations never change, so a redelivered or repeated outcome is a no-op
// and reports applied = false (C1.2.14). A repeat must name the same outcome,
// courier or release reason. An outcome that contradicts Credit's records
// changes nothing and fails with ErrOutcomeMismatch, ErrReservationNotFound or
// ErrReservationNotAssigned.
func (s *Service) ApplyOutcome(ctx context.Context, o OrderOutcome) (out Reservation, applied bool, err error) {
	if err := o.validate(); err != nil {
		return Reservation{}, false, err
	}

	err = s.repo.Within(ctx, func(tx Tx) error {
		if err := tx.LockOrder(ctx, o.OrderID); err != nil {
			return err
		}
		r, err := tx.Reservation(ctx, o.OrderID)
		if err != nil {
			return err
		}
		if r != nil && r.RequesterID != "" && r.RequesterID != o.RequesterID {
			return ErrOutcomeMismatch
		}

		now := s.clock().UTC()

		// Release outcomes return the hold, or leave a tombstone.
		if reason, ok := o.releaseReason(); ok {
			if r != nil && r.Status == Settled {
				return ErrOutcomeMismatch
			}
			if r != nil && r.Status == Released && r.Settlement.ReleaseReason != reason {
				return ErrOutcomeMismatch
			}
			out, applied, err = release(ctx, tx, o.OrderID, r, reason, now)
			return err
		}

		// Payout outcomes settle an Assigned reservation.
		if r == nil {
			return ErrReservationNotFound
		}
		switch r.Status {
		case Settled:
			if r.Settlement.Outcome != o.settlementOutcome() {
				return ErrOutcomeMismatch
			}
			a, err := tx.Assignment(ctx, r.OrderID, r.AssignmentID)
			if err != nil {
				return err
			}
			if a == nil || a.CourierID != o.CourierID {
				return ErrOutcomeMismatch
			}
			out = *r
			return nil
		case Released:
			return ErrOutcomeMismatch
		case Held:
			return ErrReservationNotAssigned
		}

		out, err = settle(ctx, tx, *r, o, s.payout, now)
		applied = err == nil
		return err
	})

	if err != nil {
		return Reservation{}, false, err
	}
	return out, applied, nil
}

// settle pays the courier and returns the rest of the hold to the requester.
// payout decides the courier's share of the accepted offer; any hold above it
// is refunded, so the requester's final cost equals the payout (C1.2.6).
func settle(
	ctx context.Context,
	tx Tx,
	r Reservation,
	o OrderOutcome,
	payout PayoutRule,
	now time.Time,
) (Reservation, error) {
	a, err := tx.Assignment(ctx, r.OrderID, r.AssignmentID)
	if err != nil {
		return Reservation{}, err
	}
	if a == nil || a.Status != Applied {
		return Reservation{}, ErrReservationNotAssigned
	}
	if a.CourierID != o.CourierID {
		return Reservation{}, ErrOutcomeMismatch
	}

	// Compute the payout from the accepted offer. The rule may come from
	// outside Credit later, so never pay more than the hold.
	payable := payout(a.OfferAmount, o)
	if payable < 0 || payable > r.ReservedAmount {
		return Reservation{}, ErrOutcomeMismatch
	}

	settlement := &Settlement{Outcome: o.settlementOutcome(), SettledAt: now}
	if o.Type == OrderCompleted {
		settlement.Completion = &Completion{
			PickedUpAt:       o.PickedUpAt,
			DeliveredAt:      o.DeliveredAt,
			DeliveryDuration: o.DeliveryDuration,
		}
	}
	settlement.PaidToCourier = payable
	settlement.ReturnedToRequester = r.ReservedAmount - payable

	// Store the settlement and move the credits.
	if _, err := tx.LockAccounts(ctx, now, r.RequesterID, a.CourierID); err != nil {
		return Reservation{}, err
	}

	next := r
	next.Status = Settled
	next.ReservedAmount = 0
	next.Settlement = settlement
	if next, err = update(ctx, tx, r, next); err != nil {
		return Reservation{}, err
	}

	err = tx.Post(ctx, now,
		Entry{
			UserID:        r.RequesterID,
			Kind:          account.Payout,
			BalanceDelta:  -payable,
			ReservedDelta: -payable,
			OrderID:       r.OrderID,
		},
		Entry{
			UserID:        r.RequesterID,
			Kind:          account.Refund,
			ReservedDelta: -settlement.ReturnedToRequester,
			OrderID:       r.OrderID,
		},
		Entry{
			UserID:       a.CourierID,
			Kind:         account.Payout,
			BalanceDelta: payable,
			OrderID:      r.OrderID,
		},
	)
	return next, err
}

func (o *OrderOutcome) validate() error {
	ids := []*string{&o.OrderID, &o.RequesterID}
	if _, release := o.releaseReason(); !release {
		ids = append(ids, &o.CourierID)
	}
	if err := canonicalIDs(ids...); err != nil {
		return err
	}
	switch o.Type {
	case OrderCompleted:
		// PostgreSQL keeps microseconds; settle on the values it will store.
		o.PickedUpAt = o.PickedUpAt.UTC().Truncate(time.Microsecond)
		o.DeliveredAt = o.DeliveredAt.UTC().Truncate(time.Microsecond)
		o.DeliveryDuration = o.DeliveryDuration.Truncate(time.Microsecond)
		if o.PickedUpAt.IsZero() || o.DeliveredAt.Before(o.PickedUpAt) || o.DeliveryDuration <= 0 {
			return ErrInvalidArgument
		}
	case OrderCancelledAfterPickup, OrderCancelledBeforePickup, OrderExpired, OrderFailed:
	default:
		return ErrInvalidArgument
	}
	return nil
}

func (o OrderOutcome) releaseReason() (ReleaseReason, bool) {
	switch o.Type {
	case OrderCancelledBeforePickup:
		return CancelledBeforePickup, true
	case OrderExpired:
		return Expired, true
	case OrderFailed:
		return Failed, true
	}
	return "", false
}

func (o OrderOutcome) settlementOutcome() SettlementOutcome {
	if o.Type == OrderCompleted {
		return OutcomeCompleted
	}
	return OutcomeCancelledAfterPickup
}
