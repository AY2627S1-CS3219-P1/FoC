package order

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
)

type AssignmentOperations interface {
	MakeOffer(context.Context, app.Caller, string, int64, string) (app.Offer, error)
	AcceptOrder(context.Context, app.Caller, string, string) (app.AssignmentResult, error)
	AcceptOffer(context.Context, app.Caller, string) (app.AssignmentResult, error)
}

func (s *Server) MakeOffer(ctx context.Context, req *connect.Request[pb.MakeOfferRequest]) (*connect.Response[pb.MakeOfferResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" || caller.Suspended {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.assignments == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	m := req.Msg
	result, err := s.assignments.MakeOffer(ctx, caller, m.OrderId, m.Credits, m.IdempotencyKey)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.MakeOfferResponse{Offer: shared.Offer(result)}), nil
}
