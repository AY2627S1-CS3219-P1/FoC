// Package reservation holds a requester's credits for an order, applies and
// reverts accepted offers, and settles or releases the hold exactly once.
package reservation

import (
	"errors"
	"time"
)

// Each error matches a reason in the credit.v1 reservation contract, except
// ErrOutcomeMismatch, ErrInvalidArgument and ErrAborted.
var (
	ErrInvalidArgument        = errors.New("invalid argument")
	ErrInsufficientCredits    = errors.New("insufficient credits")
	ErrIdempotencyConflict    = errors.New("idempotency conflict")
	ErrReservationNotFound    = errors.New("reservation not found")
	ErrReservationReleased    = errors.New("reservation released")
	ErrReservationSettled     = errors.New("reservation settled")
	ErrReservationNotAssigned = errors.New("reservation not assigned")
	ErrAdjustmentPending      = errors.New("another adjustment is applied")
	ErrAdjustmentReverted     = errors.New("adjustment reverted")
	// An order outcome disagrees with Credit's records. Nothing is changed;
	// the order needs reconciliation.
	ErrOutcomeMismatch = errors.New("order outcome does not match reservation")
	// The transaction was chosen as a deadlock victim and may be retried.
	ErrAborted = errors.New("aborted")
)

type Status string

const (
	Held     Status = "HELD"
	Assigned Status = "ASSIGNED"
	Released Status = "RELEASED"
	Settled  Status = "SETTLED"
)

type ReleaseReason string

const (
	CancelledBeforePickup ReleaseReason = "CANCELLED_BEFORE_PICKUP"
	Expired               ReleaseReason = "EXPIRED"
	Failed                ReleaseReason = "FAILED"
)

func (r ReleaseReason) valid() bool {
	return r == CancelledBeforePickup || r == Expired || r == Failed
}

type SettlementOutcome string

const (
	OutcomeReleased             SettlementOutcome = "RELEASED"
	OutcomeCompleted            SettlementOutcome = "COMPLETED"
	OutcomeCancelledAfterPickup SettlementOutcome = "CANCELLED_AFTER_PICKUP"
)

type AdjustmentStatus string

const (
	Applied  AdjustmentStatus = "APPLIED"
	Reverted AdjustmentStatus = "REVERTED"
)

// Reservation is keyed by the Order Service order ID. A tombstone (an order
// released before Credit saw it) is Released with no requester.
type Reservation struct {
	OrderID     string
	RequesterID string
	Status      Status
	// Amount at Reserve. Zero for a tombstone.
	OriginalAmount int64
	// Current hold: max(OriginalAmount, offer) while Assigned, zero once
	// terminal.
	ReservedAmount int64
	// The applied assignment while Assigned, and the paid one once Settled.
	AssignmentID string
	// Set while Assigned.
	Adjustment *Adjustment
	// Set once terminal.
	Settlement *Settlement
	Revision   int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Adjustment is the accepted offer applied to an Assigned reservation.
type Adjustment struct {
	AssignmentID string
	CourierID    string
	// Restored by RevertAdjustment.
	PreviousAmount int64
	OfferAmount    int64
	Status         AdjustmentStatus
}

type Settlement struct {
	Outcome             SettlementOutcome
	PaidToCourier       int64
	ReturnedToRequester int64
	SettledAt           time.Time
	// Set when Outcome is OutcomeReleased.
	ReleaseReason ReleaseReason
	// Lateness inputs, set when Outcome is OutcomeCompleted.
	Completion *Completion
}

type Completion struct {
	PickedUpAt       time.Time
	DeliveredAt      time.Time
	DeliveryDuration time.Duration
}

// Assignment is an offer Order applied or reverted. A reverted assignment
// without a courier is a tombstone for an offer reverted before it was applied.
type Assignment struct {
	OrderID      string
	AssignmentID string
	CourierID    string
	OfferAmount  int64
	Status       AdjustmentStatus
	CreatedAt    time.Time
	RevertedAt   *time.Time
}

// OutcomeType is how an order ended, from Order's order.outcome event.
type OutcomeType string

const (
	OrderCompleted             OutcomeType = "COMPLETED"
	OrderCancelledBeforePickup OutcomeType = "CANCELLED_BEFORE_PICKUP"
	OrderCancelledAfterPickup  OutcomeType = "CANCELLED_AFTER_PICKUP"
	OrderExpired               OutcomeType = "EXPIRED"
	OrderFailed                OutcomeType = "FAILED"
)

// OrderOutcome is the transport-neutral form of an order.outcome event.
type OrderOutcome struct {
	OrderID     string
	Type        OutcomeType
	RequesterID string
	// Set for outcomes that pay a courier.
	CourierID string
	// Lateness inputs for OrderCompleted.
	PickedUpAt       time.Time
	DeliveredAt      time.Time
	DeliveryDuration time.Duration
}
