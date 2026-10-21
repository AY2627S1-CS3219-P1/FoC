package order

import (
	"encoding/json"
	"time"
)

// Audit records and durable facts describe the same aggregate revision.
type AuditEntry struct {
	PreviousStatus, Status Status
	At                     time.Time
	Details                json.RawMessage
}

type FactKind string

const (
	StatusChanged  FactKind = "order.status_changed"
	Outcome        FactKind = "order.outcome"
	CourierWarning FactKind = "order.courier_warning_requested"
)

type Fact struct {
	ID      string
	Kind    FactKind
	Version int32
	Payload json.RawMessage
}

// FactsPayload is a versioned domain contract, independent of RPC and Kafka.
// Credit owns payable reward calculation. Amounts here are observations only.
type FactsPayload struct {
	PreviousCourierID  *string    `json:"previous_courier_id,omitempty"`
	OrderID            string     `json:"order_id"`
	Revision           int64      `json:"revision"`
	RequesterID        string     `json:"requester_id"`
	CourierID          *string    `json:"courier_id"`
	PreviousStatus     Status     `json:"previous_status,omitempty"`
	Status             Status     `json:"status"`
	Action             Action     `json:"action"`
	ReservedCredits    int64      `json:"reserved_credits"`
	DeliveryDurationNS int64      `json:"delivery_duration_ns"`
	PickedUpAt         *time.Time `json:"picked_up_at,omitempty"`
	DeliveredAt        *time.Time `json:"delivered_at,omitempty"`
	AfterPickup        bool       `json:"after_pickup"`
	At                 time.Time  `json:"at"`
}

// NewChange builds mandatory audit/facts. Creation is expected revision zero,
// Pending Reservation only. Metadata changes produce an audit but no status fact.
func NewChange(before *Order, after Order, actor Actor, action Action, details json.RawMessage, newID func() string) (Change, error) {
	// Establish the prior revision before checking the lifecycle and actor.
	expected := int64(0)
	previous := Status("")
	if before != nil {
		expected = before.Revision
		previous = before.Status
		if before.ID != after.ID {
			return Change{}, ErrInvalid
		}
	}

	if after.Revision <= 0 || after.Revision != expected+1 || !after.Status.IsValid() || (before == nil && (after.Status != PendingReservation || action != Create)) {
		return Change{}, ErrInvalid
	}
	if actor.Kind != System && ((actor.Kind != User && actor.Kind != Admin) || actor.ID == "") {
		return Change{}, ErrForbidden
	}

	// Creation, status transitions and metadata amendments have distinct rules.
	if before == nil {
		if actor.Kind != User || !after.IsRequestedBy(actor.ID) {
			return Change{}, ErrForbidden
		}
	} else if previous != after.Status {
		next, repeat, err := Transition(*before, action, actor)
		if err != nil {
			return Change{}, err
		}
		if repeat || next != after.Status {
			return Change{}, ErrPrecondition
		}
		if action == Assign && !after.IsAssignedTo(actor.ID) {
			return Change{}, ErrInvalid
		}
		if action == AcceptCounteroffer && (after.CourierID == nil || *after.CourierID == "" || after.IsAssignedTo(before.RequesterID)) {
			return Change{}, ErrInvalid
		}
	} else {
		// Metadata actions are explicit, not same-status lifecycle repeats.
		// AssignmentTx additionally checks offers and immutable attempt ownership.
		switch action {
		case BeginWithdrawal:
			if actor.Kind != User || before.Status != AwaitingPickup || !before.IsAssignedTo(actor.ID) || before.PendingCancellationID != nil || after.PendingCancellationID == nil {
				return Change{}, ErrPrecondition
			}
		case RecordItemLocation:
			if actor.Kind != User || !before.IsAssignedTo(actor.ID) || before.PickedUpAt == nil || (before.Status != Cancelled && before.Status != Failed) {
				return Change{}, ErrPrecondition
			}
		case ProposeOffer:
			if actor.Kind != User || before.Status != AwaitingCourier || before.IsRequestedBy(actor.ID) {
				return Change{}, ErrForbidden
			}
		case BeginAssignment:
			if actor.Kind != User || before.Status != AwaitingCourier {
				return Change{}, ErrForbidden
			}
		case ResolveAssignment:
			if actor.Kind != User {
				return Change{}, ErrForbidden
			}
		default:
			return Change{}, ErrPrecondition
		}
	}

	if before != nil {
		if err := validateFields(*before, after, action); err != nil {
			return Change{}, err
		}
	}

	// Every accepted change carries bounded, valid audit details.
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	if len(details) > 4096 || !json.Valid(details) {
		return Change{}, ErrInvalid
	}

	c := Change{
		ExpectedRevision: expected,
		Order:            after,
		Actor:            actor,
		Action:           action,
		Audit: AuditEntry{
			PreviousStatus: previous,
			Status:         after.Status,
			At:             after.UpdatedAt,
			Details:        details,
		},
	}
	if previous == after.Status {
		return c, nil
	}

	// Status facts preserve observations needed by downstream outcome consumers.
	var previousCourierID *string
	if before != nil {
		previousCourierID = before.CourierID
	}

	payload, err := json.Marshal(FactsPayload{
		PreviousCourierID:  previousCourierID,
		OrderID:            after.ID,
		Revision:           after.Revision,
		RequesterID:        after.RequesterID,
		CourierID:          after.CourierID,
		PreviousStatus:     previous,
		Status:             after.Status,
		Action:             action,
		ReservedCredits:    after.ReservedCredits,
		DeliveryDurationNS: int64(after.DeliveryDuration),
		PickedUpAt:         after.PickedUpAt,
		DeliveredAt:        after.DeliveredAt,
		AfterPickup:        after.PickedUpAt != nil,
		At:                 after.UpdatedAt,
	})
	if err != nil {
		return Change{}, err
	}

	add := func(k FactKind) { c.Facts = append(c.Facts, Fact{ID: newID(), Kind: k, Version: 1, Payload: payload}) }
	add(StatusChanged)
	if after.Status == Completed || after.Status == Cancelled || after.Status == Expired || after.Status == Failed {
		add(Outcome)
	}
	if previous == Delivering && ((action == Cancel && actor.Kind == User && after.IsAssignedTo(actor.ID)) ||
		(action == Timeout && actor.Kind == System && after.PickedUpAt != nil && after.CourierID != nil)) {
		add(CourierWarning)
	}

	return c, nil
}
