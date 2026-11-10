// Package orderinternal serves private service-to-service participant reads.
package orderinternal

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

type Operations interface {
	GetOrderParticipants(context.Context, string) (app.Order, error)
}

type Server struct {
	orderv1connect.UnimplementedOrderInternalServiceHandler
	operations Operations
}

func NewServer(operations Operations) *Server { return &Server{operations: operations} }

func (s *Server) GetOrderParticipants(
	ctx context.Context,
	req *connect.Request[pb.GetOrderParticipantsRequest],
) (*connect.Response[pb.GetOrderParticipantsResponse], error) {
	o, err := s.operations.GetOrderParticipants(ctx, req.Msg.GetOrderId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.GetOrderParticipantsResponse{
		OrderId:     o.ID,
		RequesterId: o.RequesterID,
		CourierId:   o.CourierID,
		Status:      shared.OrderStatus(o.Status),
	}), nil
}
