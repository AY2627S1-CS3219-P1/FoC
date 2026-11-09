package order

import (
	"context"
	"time"
)

const (
	AcceptOrderMethod = "/order.v1.OrderService/AcceptOrder"
	MakeOfferMethod   = "/order.v1.OrderService/MakeOffer"

	ProposeOffer       Action = "propose_offer"
	BeginAssignment    Action = "begin_assignment"
	ResolveAssignment  Action = "resolve_assignment"
	AcceptCounteroffer Action = "accept_counteroffer"
)

type AssignmentPhase string

const (
	AdjustmentPending   AssignmentPhase = "adjustment_pending"
	RevertPending       AssignmentPhase = "revert_pending"
	AssignmentSucceeded AssignmentPhase = "succeeded"
	AssignmentRejected  AssignmentPhase = "rejected"
)

// AssignmentAttempt owns immutable financial intent and durable recovery state.
// ID is also Credit's offer_id: a rejected attempt must not fence a later attempt
// to accept the same public offer after the requester obtains more credits.
type AssignmentAttempt struct {
	ID, OrderID, OfferID, CallerID, CourierID string
	Method, Key, RequestHash                  string
	Amount, OriginalAmount                    int64
	Phase                                     AssignmentPhase
	Generation                                int64
	Attempts                                  int32
	LeaseUntil, NextAttemptAt                 time.Time
	Rejection                                 string
	CreatedAt, UpdatedAt                      time.Time
}

func (a AssignmentAttempt) IsResolved() bool {
	return a.Phase == AssignmentSucceeded || a.Phase == AssignmentRejected
}

// AssignmentResult separates the historical outcome from the current Order.
// A replay never claims that a courier remains assigned after withdrawal.
type AssignmentResult struct {
	Order     Order
	Offer     Offer
	AttemptID string
	Phase     AssignmentPhase
	Rejection string
}

type AdjustmentIntent struct {
	OrderID, OfferID, CourierID string
	Amount                      int64
}

type AssignmentReservation struct {
	OrderID, RequesterID           string
	OriginalAmount, ReservedAmount int64
	State                          string
	OfferID, CourierID             string
}

// AssignmentCredit is separate from creation's intentionally narrower view.
// Mutation replies can be historical. Recovery confirms current state by read.
type AssignmentCredit interface {
	Adjust(context.Context, AdjustmentIntent) error
	Revert(context.Context, string, string) error
	AssignmentReservation(context.Context, string) (AssignmentReservation, error)
}

type AssignmentTx interface {
	Tx
	IdempotencyTx
	HasRejectedAcceptance(context.Context, string, string) (bool, error)
	FindAssignment(context.Context, string) (AssignmentAttempt, error)
	ActiveAssignment(context.Context, string) (*AssignmentAttempt, error)
	OfferAssignment(context.Context, string) (*AssignmentAttempt, error)
	InsertAssignment(context.Context, AssignmentAttempt) error
	SaveAssignment(context.Context, AssignmentAttempt, int64) error
}

type AssignmentRepository interface {
	WithinAssignment(context.Context, func(AssignmentTx) error) error
	AssignmentOrderID(context.Context, string) (string, error)
	OfferOrderID(context.Context, string) (string, error)
	DueAssignments(context.Context, time.Time, int32) ([]string, error)
}

type AssignmentOptions struct {
	Now   func() time.Time
	NewID func() string
}
