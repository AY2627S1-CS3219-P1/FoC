package orderinternal

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

type SuspensionOperations interface {
	Apply(context.Context, app.AccountSuspensionDecision) error
}

type Server struct {
	orderv1connect.UnimplementedOrderInternalServiceHandler
	Operations SuspensionOperations
}

func (s *Server) ApplyAccountSuspension(ctx context.Context, req *connect.Request[pb.ApplyAccountSuspensionRequest]) (*connect.Response[pb.ApplyAccountSuspensionResponse], error) {
	if s.Operations == nil {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	m := req.Msg
	decision := app.AccountSuspensionDecision{
		ID:        m.DecisionId,
		UserID:    m.UserId,
		AdminID:   m.AdminId,
		Revision:  m.Revision,
		Suspended: m.Suspended,
		Reason:    m.Reason,
	}
	if err := s.Operations.Apply(ctx, decision); err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ApplyAccountSuspensionResponse{DecisionId: m.DecisionId}), nil
}
