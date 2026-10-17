package review

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

// ConnectOrderGateway reads orders from the order service's internal
// OrderInternalService over Connect. Unknown orders map to errOrderNotFound;
// every other failure maps to errOrderUnavailable so callers fail closed
// instead of proceeding on a stale or absent answer.
type ConnectOrderGateway struct {
	client orderv1connect.OrderInternalServiceClient
}

func NewConnectOrderGateway(client orderv1connect.OrderInternalServiceClient) *ConnectOrderGateway {
	return &ConnectOrderGateway{client: client}
}

func (g *ConnectOrderGateway) GetOrderParticipants(ctx context.Context, orderID uuid.UUID) (OrderParticipants, error) {
	resp, err := g.client.GetOrderParticipants(ctx, connect.NewRequest(&orderv1.GetOrderParticipantsRequest{OrderId: orderID.String()}))
	if err != nil {
		if connect.CodeOf(err) == connect.CodeNotFound {
			return OrderParticipants{}, errOrderNotFound
		}
		return OrderParticipants{}, fmt.Errorf("%w: %v", errOrderUnavailable, err)
	}
	requester, err := uuid.Parse(resp.Msg.GetRequesterId())
	if err != nil {
		return OrderParticipants{}, fmt.Errorf("%w: malformed requester id", errOrderUnavailable)
	}
	participants := OrderParticipants{
		RequesterID: requester,
		Completed:   resp.Msg.GetStatus() == orderv1.OrderStatus_ORDER_STATUS_COMPLETED,
	}
	if courier := resp.Msg.GetCourierId(); courier != "" {
		id, err := uuid.Parse(courier)
		if err != nil {
			return OrderParticipants{}, fmt.Errorf("%w: malformed courier id", errOrderUnavailable)
		}
		participants.CourierID = id
	}
	return participants, nil
}
