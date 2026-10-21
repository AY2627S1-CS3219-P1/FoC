package order

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Progress runs the participant actions that advance an assigned order with
// local writes only. Settlement of a completed order is driven by its outcome
// fact, so ConfirmReceipt does not call Credit.
type Progress struct {
	mutator *Mutator
	now     func() time.Time
	newID   func() string
}

func NewProgress(mutator *Mutator) *Progress {
	return &Progress{mutator: mutator, now: time.Now, newID: uuid.NewString}
}

// ConfirmPickup moves the assigned courier's order from Awaiting Pickup to
// Delivering. A repeat by that courier returns the order without writing.
func (p *Progress) ConfirmPickup(ctx context.Context, c Caller, id string) (Order, error) {
	return p.advance(ctx, c, id, Pickup, func(o *Order, now time.Time) { o.PickedUpAt = &now })
}

// ConfirmReceipt moves the requester's order from Delivered to Completed. A
// repeat by the requester returns the completed order without writing.
func (p *Progress) ConfirmReceipt(ctx context.Context, c Caller, id string) (Order, error) {
	return p.advance(ctx, c, id, ConfirmReceipt, func(o *Order, now time.Time) { o.CompletedAt = &now })
}

// advance applies one caller action to the locked order. stamp records the
// action's own timestamp on the copy that becomes the next revision.
func (p *Progress) advance(ctx context.Context, c Caller, id string, action Action, stamp func(*Order, time.Time)) (Order, error) {
	if c.ID == "" {
		return Order{}, ErrForbidden
	}
	if _, err := parseID(id); err != nil {
		return Order{}, err
	}

	var result Order
	err := p.mutator.Mutate(ctx, id, func(current Order, _ []Offer) (*Change, error) {
		// Postgres keeps microseconds; truncate so the response matches a reread.
		now := p.now().UTC().Truncate(time.Microsecond)
		if !visibleTo(current, c, now) {
			return nil, ErrNotFound
		}
		// A suspended courier may finish an existing delivery, but cannot take
		// custody of an item. A persisted pickup repeat remains a read of the
		// earlier result; receipt is still available to a suspended requester.
		if action == Pickup && current.Status == AwaitingPickup && c.Suspended {
			return nil, ErrForbidden
		}

		actor := Actor{Kind: User, ID: c.ID}
		next, repeat, err := Transition(current, action, actor)
		if err != nil {
			return nil, err
		}
		if repeat {
			result = current
			return nil, nil
		}

		after := cloneOrder(current)
		after.Status = next
		after.Revision++
		after.UpdatedAt = now
		stamp(&after, now)

		change, err := NewChange(&current, after, actor, action, nil, p.newID)
		if err != nil {
			return nil, err
		}
		result = after
		return &change, nil
	})
	if err != nil {
		return Order{}, err
	}

	return result, nil
}
