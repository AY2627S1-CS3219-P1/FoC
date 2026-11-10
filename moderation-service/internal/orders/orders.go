// Package orders reads an order's participants from Order Service's internal
// OrderInternalService, so reviews, reports and appeals can check that their
// users took part in the order (#301). Ported from #156, where it lived in the
// review package.
//
// The lookup carries no user identity. The response depends only on the order
// ID, and Order's internal listener is reachable only by User and
// Communication, so network reachability is the trust boundary.
package orders

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

var (
	// ErrNotFound reports an order Order does not know.
	ErrNotFound = errors.New("order does not exist")
	// ErrUnavailable reports that the participants could not be read, or the
	// answer was malformed. Callers fail closed.
	ErrUnavailable = errors.New("order validation unavailable")
)

// Participants is User Service's view of an order's participants.
type Participants struct {
	RequesterID uuid.UUID
	// CourierID is uuid.Nil while no courier is assigned.
	CourierID uuid.UUID
	Completed bool
}

// HasParticipant reports whether userID is the requester or the current
// courier.
func (p Participants) HasParticipant(userID uuid.UUID) bool {
	if userID == uuid.Nil {
		return false
	}
	return userID == p.RequesterID || userID == p.CourierID
}

// Contains reports whether a and b are exactly the order's requester and
// courier, in either order. An order without a courier has no such pair.
func (p Participants) Contains(a, b uuid.UUID) bool {
	if p.CourierID == uuid.Nil {
		return false
	}
	return (a == p.RequesterID && b == p.CourierID) || (a == p.CourierID && b == p.RequesterID)
}

// Gateway reads an order's participants.
type Gateway interface {
	GetOrderParticipants(ctx context.Context, orderID uuid.UUID) (Participants, error)
}

// ExternalError maps a lookup failure to the error callers return: not found
// for an unknown order, unavailable when Order cannot answer.
func ExternalError(err error) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return errs.WrapNotFoundError(err, "order does not exist")
	case errors.Is(err, ErrUnavailable):
		return errs.WrapUnavailableError(err, "order validation is temporarily unavailable")
	default:
		return err
	}
}

// ConnectGateway calls OrderInternalService over Connect.
type ConnectGateway struct {
	client orderv1connect.OrderInternalServiceClient
}

func NewConnectGateway(client orderv1connect.OrderInternalServiceClient) *ConnectGateway {
	return &ConnectGateway{client: client}
}

// GetOrderParticipants maps Connect's not found to ErrNotFound. Every other
// failure, and any response that does not name this order with well-formed
// participants, is ErrUnavailable.
func (g *ConnectGateway) GetOrderParticipants(ctx context.Context, orderID uuid.UUID) (Participants, error) {
	resp, err := g.client.GetOrderParticipants(
		ctx,
		connect.NewRequest(&orderv1.GetOrderParticipantsRequest{OrderId: orderID.String()}),
	)
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return Participants{}, ErrNotFound
		}
		return Participants{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	if resp.Msg.GetOrderId() != orderID.String() {
		return Participants{}, fmt.Errorf("%w: mismatched order id", ErrUnavailable)
	}
	requester, err := uuid.Parse(resp.Msg.GetRequesterId())
	if err != nil || requester == uuid.Nil {
		return Participants{}, fmt.Errorf("%w: malformed requester id", ErrUnavailable)
	}
	participants := Participants{
		RequesterID: requester,
		Completed:   resp.Msg.GetStatus() == orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
	}
	if courier := resp.Msg.GetCourierId(); courier != "" {
		id, err := uuid.Parse(courier)
		if err != nil || id == uuid.Nil {
			return Participants{}, fmt.Errorf("%w: malformed courier id", ErrUnavailable)
		}
		participants.CourierID = id
	}
	return participants, nil
}

// UnavailableGateway fails closed for every order. The composition root uses
// it while ORDER_SERVICE_BASE_URL is unset.
type UnavailableGateway struct{}

func (UnavailableGateway) GetOrderParticipants(context.Context, uuid.UUID) (Participants, error) {
	return Participants{}, ErrUnavailable
}
