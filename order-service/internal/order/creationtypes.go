package order

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrAlreadyExists = errors.New("idempotency key was already used with different input")
	ErrUnavailable   = errors.New("creation dependency unavailable")
	ErrRateLimited   = errors.New("creation limit exceeded; retry later")
	// Only a typed Credit business refusal permits rejection without compensation.
	ErrCreditInsufficient = errors.New("credit reservation definitively refused")
)

type CreationRejection string

const (
	InsufficientCredits CreationRejection = "insufficient_credits"
	CreationExpired     CreationRejection = "creation_expired"
	ReservationReleased CreationRejection = "reservation_released"
)

func (r CreationRejection) Error() string {
	switch r {
	case InsufficientCredits:
		return "insufficient credits"
	case CreationExpired:
		return "order creation expired"
	case ReservationReleased:
		return "credit reservation was released"
	default:
		return "order creation rejected"
	}
}

func (r CreationRejection) Unwrap() error { return ErrPrecondition }

// CreateInput contains business values only. The key is excluded from canonical
// payload comparison; credentials belong exclusively to outbound request context.
type CreateInput struct {
	Key              string        `json:"-"`
	PickupID         string        `json:"pickup_id"`
	DeliveryID       string        `json:"delivery_id"`
	Instructions     *string       `json:"instructions"`
	ItemDescription  string        `json:"item_description"`
	ImageIDs         []string      `json:"item_image_ids"`
	RewardCredits    int64         `json:"reward_credits"`
	DeliveryDuration time.Duration `json:"delivery_duration_ns"`
	ExpiresAt        time.Time     `json:"expires_at"`
}

type SelectableLocation struct {
	Snapshot LocationSnapshot
	Archived bool
}

type CreationLocations interface {
	GetLocation(context.Context, string) (SelectableLocation, error)
}

type ReservationIntent struct {
	OrderID, RequesterID string
	Amount               int64
}

type ReservationState string

const (
	ReservationHeld       ReservationState = "held"
	ReservationIsReleased ReservationState = "released"
)

// ReservationView is always obtained with GetReservation, never a historical
// mutation response. Unsupported states are rejected by the Credit adapter.
type ReservationView struct {
	OrderID, RequesterID           string
	State                          ReservationState
	OriginalAmount, ReservedAmount int64
}

type CreationCredit interface {
	Reserve(context.Context, ReservationIntent) error
	GetReservation(context.Context, string) (ReservationView, error)
	ReleaseExpired(context.Context, string) error
}

type CreationPhase string

const (
	ReservePending   CreationPhase = "reserve_pending"
	AbortPending     CreationPhase = "abort_pending"
	CreationOpened   CreationPhase = "opened"
	CreationRejected CreationPhase = "rejected"
)

type CreationWorkflow struct {
	ID, OrderID, RequesterID, Hash string
	Canonical                      json.RawMessage
	Amount                         int64
	ExpiresAt                      time.Time
	Phase                          CreationPhase
	Generation                     int64
	Attempts                       int32
	LeaseUntil, NextAttemptAt      time.Time
	LastError                      string
	Rejection                      CreationRejection
	Result                         *Order
	CreatedAt, UpdatedAt           time.Time
}

func (w CreationWorkflow) IsResolved() bool {
	return w.Phase == CreationOpened || w.Phase == CreationRejected
}

func (w CreationWorkflow) Intent() ReservationIntent {
	return ReservationIntent{OrderID: w.OrderID, RequesterID: w.RequesterID, Amount: w.Amount}
}

// Creation transactions share the aggregate Apply boundary. Lock Order first,
// then load its workflow and relationships in subsequent statements.
type CreationTx interface {
	Tx
	IdempotencyTx
	FindCreationKey(context.Context, string, string, time.Time) (*CreationReplay, error)
	CountRecentCreations(context.Context, string, time.Time) (int64, error)
	InsertCreation(context.Context, CreationWorkflow, string) error
	TryLockCreationOrder(context.Context, string) (Order, error)
	LockCreation(context.Context, string) (CreationWorkflow, error)
	SaveCreation(context.Context, CreationWorkflow, int64) error
	AttachCreationImages(context.Context, string, string, []ItemImageUpload) error
}

type CreationRepository interface {
	WithinCreation(context.Context, func(CreationTx) error) error
	Creation(context.Context, string) (CreationWorkflow, error)
	DueCreations(context.Context, time.Time, int32) ([]string, error)
	HasPendingCreations(context.Context) (bool, error)
}

type CreationOptions struct {
	Images            *ItemImages
	AdmissionsEnabled bool
	Now               func() time.Time
	NewID             func() string
}
