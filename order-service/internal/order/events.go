package order

import (
	"encoding/json"
	"time"
)

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
	OrderID                 string  `json:"order_id"`
	Revision                int64   `json:"revision"`
	RequesterID             string  `json:"requester_id"`
	CourierID               *string `json:"courier_id"`
	PreviousStatus          Status  `json:"previous_status,omitempty"`
	Status                  Status  `json:"status"`
	Action                  Action  `json:"action"`
	ReservedCredits         int64   `json:"reserved_credits"`
	DeliveryDurationNS      int64   `json:"delivery_duration_ns"`
	PickedUpAt, DeliveredAt *time.Time
	AfterPickup             bool      `json:"after_pickup"`
	At                      time.Time `json:"at"`
}

// NewChange builds mandatory audit/facts. Creation is expected revision zero,
// Pending Reservation only. Metadata changes produce an audit but no status fact.
func NewChange(before *Order, after Order, actor Actor, action Action, details json.RawMessage, newID func() string) (Change, error) {
	expected := int64(0)
	previous := Status("")
	if before != nil {
		expected = before.Revision
		previous = before.Status
		if before.ID != after.ID {
			return Change{}, ErrInvalid
		}
	}
	if after.Revision != expected+1 || !after.Status.Valid() || (before == nil && (after.Status != PendingReservation || action != Create)) {
		return Change{}, ErrInvalid
	}
	if len(details) == 0 {
		details = json.RawMessage(`{}`)
	}
	if len(details) > 4096 || !json.Valid(details) {
		return Change{}, ErrInvalid
	}
	c := Change{ExpectedRevision: expected, Order: after, Actor: actor, Action: action, Audit: AuditEntry{PreviousStatus: previous, Status: after.Status, At: after.UpdatedAt, Details: details}}
	if previous == after.Status {
		return c, nil
	}
	payload, err := json.Marshal(FactsPayload{OrderID: after.ID, Revision: after.Revision, RequesterID: after.RequesterID, CourierID: after.CourierID, PreviousStatus: previous, Status: after.Status, Action: action, ReservedCredits: after.ReservedCredits, DeliveryDurationNS: int64(after.DeliveryDuration), PickedUpAt: after.PickedUpAt, DeliveredAt: after.DeliveredAt, AfterPickup: after.PickedUpAt != nil, At: after.UpdatedAt})
	if err != nil {
		return Change{}, err
	}
	add := func(k FactKind) { c.Facts = append(c.Facts, Fact{ID: newID(), Kind: k, Version: 1, Payload: payload}) }
	add(StatusChanged)
	if after.Status == Completed || after.Status == Cancelled || after.Status == Expired || after.Status == Failed {
		add(Outcome)
	}
	if previous == Delivering && action == Cancel && actor.Kind == User && after.Courier(actor.ID) {
		add(CourierWarning)
	}
	return c, nil
}
