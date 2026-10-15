package orderadmin

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

type Operations interface {
	ListUserOrders(context.Context, app.Caller, string, app.ListRequest) (app.OrderPage, error)
}

type Server struct {
	orderv1connect.UnimplementedOrderAdminServiceHandler
	operations Operations
}

func NewServer(operations Operations) *Server { return &Server{operations: operations} }

func (s *Server) ListUserOrders(ctx context.Context, req *connect.Request[pb.ListUserOrdersRequest]) (*connect.Response[pb.ListUserOrdersResponse], error) {
	m := req.Msg
	r, err := shared.ListRequest(m.GetPage(), m.GetPageSize(), m.GetStatus(), m.GetRole())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	p, err := s.operations.ListUserOrders(ctx, shared.Caller(ctx), m.GetUserId(), r)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ListUserOrdersResponse{Orders: shared.Orders(p.Orders), PageInfo: shared.PageInfo(p.PageInfo)}), nil
}
