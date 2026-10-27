package order

import (
	"errors"
	"strings"
	"time"
)

// Order lifecycle and stored location snapshots.
type Status string

const (
	PendingReservation Status = "pending_reservation"
	AwaitingCourier    Status = "awaiting_courier"
	AwaitingPickup     Status = "awaiting_pickup"
	Delivering         Status = "delivering"
	Delivered          Status = "delivered"
	Completed          Status = "completed"
	Cancelled          Status = "cancelled"
	Expired            Status = "expired"
	Failed             Status = "failed"
	Rejected           Status = "rejected"
)

// IsValid reports whether the status belongs to the order lifecycle.
func (s Status) IsValid() bool {
	switch s {
	case PendingReservation,
		AwaitingCourier,
		AwaitingPickup,
		Delivering,
		Delivered,
		Completed,
		Cancelled,
		Expired,
		Failed,
		Rejected:
		return true
	}

	return false
}

// IsTerminal reports whether ordinary lifecycle transitions have ended.
func (s Status) IsTerminal() bool {
	return s == Completed || s == Cancelled || s == Expired || s == Failed || s == Rejected
}

type LocationSnapshot struct {
	LocationID   string  `json:"location_id"`
	Name         string  `json:"name"`
	BuildingName string  `json:"building_name"`
	Floor        *string `json:"floor,omitempty"`
}

type Order struct {
	PendingCancellationID                                      *string
	ID, RequesterID                                            string
	CourierID                                                  *string
	Status                                                     Status
	Pickup, Delivery                                           LocationSnapshot
	DeliveryInstructions                                       *string
	ItemDescription                                            string
	RewardCredits, ReservedCredits                             int64
	DeliveryDuration                                           time.Duration
	ExpiresAt                                                  time.Time
	AssignedAt, PickedUpAt, DeliveredAt, CompletedAt, ClosedAt *time.Time
	CloseReason, ItemLocation                                  *string
	UnattendedDelivery                                         bool
	Revision                                                   int64
	CreatedAt, UpdatedAt                                       time.Time
}

// Courier offers retain their own status and history alongside the order.
type OfferStatus string

const (
	OfferOpen      OfferStatus = "open"
	OfferAccepting OfferStatus = "accepting"
	OfferAccepted  OfferStatus = "accepted"
	OfferVoid      OfferStatus = "void"
)

type Offer struct {
	ID, OrderID, CourierID string
	Credits                int64
	Status                 OfferStatus
	CreatedAt, UpdatedAt   time.Time
}

// Actors identify mutation authors; callers carry the current request identity.
type ActorKind string

const (
	User   ActorKind = "user"
	Admin  ActorKind = "admin"
	System ActorKind = "system"
)

type Actor struct {
	Kind ActorKind
	ID   string
}

type Caller struct {
	ID               string
	Admin, Suspended bool
}

// IsRequestedBy reports whether the user created the order.
func (o Order) IsRequestedBy(userID string) bool {
	return userID != "" && strings.EqualFold(o.RequesterID, userID)
}

// IsAssignedTo reports whether the user is the order's assigned courier.
func (o Order) IsAssignedTo(userID string) bool {
	return userID != "" && o.CourierID != nil && strings.EqualFold(*o.CourierID, userID)
}

// HasParticipant reports whether the user is the requester or assigned courier.
func (o Order) HasParticipant(userID string) bool {
	return o.IsRequestedBy(userID) || o.IsAssignedTo(userID)
}

// Action identifies the operation authorized by the lifecycle policy.
type Action string

const (
	Create             Action = "create"
	ConfirmReservation Action = "confirm_reservation"
	RefuseReservation  Action = "refuse_reservation"
	Assign             Action = "assign"
	Cancel             Action = "cancel"
	Expire             Action = "expire"
	Pickup             Action = "pickup"
	Withdraw           Action = "withdraw"
	Unassign           Action = "unassign"
	Deliver            Action = "deliver"
	Timeout            Action = "timeout"
	ConfirmReceipt     Action = "confirm_receipt"
	RecordItemLocation Action = "record_item_location"
)

// Listing filters and pagination are shared by requester, courier and admin reads.
type Role string

const (
	AnyRole       Role = ""
	RequesterRole Role = "requester"
	CourierRole   Role = "courier"
)

type Page struct{ Number, Size int32 }

func (p Page) Normalize() (Page, error) {
	if p.Number < 0 || p.Size < 0 || p.Size > 100 {
		return p, ErrInvalid
	}

	if p.Number == 0 {
		p.Number = 1
	}
	if p.Size == 0 {
		p.Size = 20
	}

	return p, nil
}

type PageInfo struct {
	Page
	Total int64
}

func (p PageInfo) TotalPages() int32 {
	if p.Total == 0 {
		return 0
	}

	return int32((p.Total-1)/int64(p.Size) + 1)
}

type ListRequest struct {
	Page                 Page
	Status               Status
	Role                 Role
	PickupID, DeliveryID string
	DeliveryBuildingName string
}

type OrderPage struct {
	Orders   []Order
	PageInfo PageInfo
}

// Domain errors are translated by the RPC boundary.
var (
	ErrNotFound     = errors.New("order not found")
	ErrForbidden    = errors.New("permission denied")
	ErrInvalid      = errors.New("invalid argument")
	ErrPrecondition = errors.New("ineligible order action")
	ErrConflict     = errors.New("revision conflict")
)
