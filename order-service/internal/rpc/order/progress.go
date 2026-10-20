package order

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
)

type ProgressOperations interface {
	ConfirmPickup(context.Context, app.Caller, string) (app.Order, error)
	ConfirmReceipt(context.Context, app.Caller, string) (app.Order, error)
}

func NewServerWithProgress(operations Operations, creator CreationOperations, images ImageOperations, progress ProgressOperations, assignments ...AssignmentOperations) *Server {
	s := NewServer(operations, creator, images, assignments...)
	s.progress = progress
	return s
}

func (s *Server) ConfirmPickup(ctx context.Context, req *connect.Request[pb.ConfirmPickupRequest]) (*connect.Response[pb.ConfirmPickupResponse], error) {
	if s.progress == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	caller := shared.Caller(ctx)
	o, err := s.progress.ConfirmPickup(ctx, caller, req.Msg.GetOrderId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := shared.OrderView(ctx, caller, o, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ConfirmPickupResponse{Order: view}), nil
}

func (s *Server) ConfirmReceipt(ctx context.Context, req *connect.Request[pb.ConfirmReceiptRequest]) (*connect.Response[pb.ConfirmReceiptResponse], error) {
	if s.progress == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	caller := shared.Caller(ctx)
	o, err := s.progress.ConfirmReceipt(ctx, caller, req.Msg.GetOrderId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := shared.OrderView(ctx, caller, o, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ConfirmReceiptResponse{Order: view}), nil
}
