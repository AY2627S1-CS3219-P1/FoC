// Package chat relays Telegram messages between an order's requester and
// courier, using a local copy of each order's participants.
package chat

import (
	"time"

	"github.com/google/uuid"
)

const (
	// GracePeriod is how long chat stays open after an order ends, for
	// follow-ups such as where an item was left.
	GracePeriod = time.Hour
	// Retention is how long messages are kept after their order ends.
	Retention = 30 * 24 * time.Hour
)

// Statuses, as stored from order.status_changed.
const (
	AwaitingPickup = "awaiting_pickup"
	Delivering     = "delivering"
	Delivered      = "delivered"
	Completed      = "completed"
	Cancelled      = "cancelled"
	Failed         = "failed"
)

// Order is the local copy of an order's participants.
type Order struct {
	ID          uuid.UUID
	RequesterID uuid.UUID
	// uuid.Nil while no courier is assigned.
	CourierID  uuid.UUID
	Status     string
	Revision   int64
	PickupName string
	// Zero until the order ends.
	EndedAt time.Time
}

// IsOpen reports whether the participants may chat: from assignment until
// delivery, and for GracePeriod after an order with a courier ends.
func (o Order) IsOpen(now time.Time) bool {
	if o.CourierID == uuid.Nil {
		return false
	}
	switch o.Status {
	case AwaitingPickup, Delivering, Delivered:
		return true
	case Completed, Cancelled, Failed:
		return !o.EndedAt.IsZero() && now.Before(o.EndedAt.Add(GracePeriod))
	}
	return false
}

// Other returns the participant who isn't userID, or false if userID isn't a
// participant.
func (o Order) Other(userID uuid.UUID) (uuid.UUID, bool) {
	switch userID {
	case o.RequesterID:
		return o.CourierID, o.CourierID != uuid.Nil
	case o.CourierID:
		return o.RequesterID, true
	}
	return uuid.Nil, false
}

// Label names the order in messages and buttons, such as "Fine Food order".
func (o Order) Label() string {
	if o.PickupName == "" {
		return "your order"
	}
	return o.PickupName + " order"
}
