package review

import (
	"context"

	"github.com/google/uuid"
)

// OrderParticipants is the review package's own view of an order's
// participants, mirroring the order service's internal contract
// (proto/order/v1/internal.proto) without importing its types into the domain.
type OrderParticipants struct {
	RequesterID uuid.UUID
	// CourierID is the zero UUID while no courier is assigned.
	CourierID uuid.UUID
	Completed bool
}

// Contains reports whether reviewer and reviewee are exactly the order's two
// participants. An unassigned courier never matches, so an order without a
// courier has no reviewable pair.
func (p OrderParticipants) Contains(reviewer, reviewee uuid.UUID) bool {
	if p.CourierID == uuid.Nil {
		return false
	}
	return (reviewer == p.RequesterID && reviewee == p.CourierID) ||
		(reviewer == p.CourierID && reviewee == p.RequesterID)
}

// OrderGateway is the order data the review service reads from the order
// service. The response is a pure function of the order id, so the call
// carries no user identity: the order service's internal listener is mounted
// separately from its public routes and never published to the internet, and
// network reachability is its trust boundary.
type OrderGateway interface {
	GetOrderParticipants(ctx context.Context, orderID uuid.UUID) (OrderParticipants, error)
}

// UnavailableOrderGateway fails closed for every order. It is the composition
// root's stand-in while ORDER_SERVICE_URL is unset: review submission is
// rejected rather than accepted without validation.
type UnavailableOrderGateway struct{}

func (UnavailableOrderGateway) GetOrderParticipants(context.Context, uuid.UUID) (OrderParticipants, error) {
	return OrderParticipants{}, errOrderUnavailable
}
