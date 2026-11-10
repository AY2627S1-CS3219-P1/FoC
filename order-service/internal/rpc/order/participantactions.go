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

type DispositionOperations interface {
	RecordItemLocation(context.Context, app.Caller, string, string) (app.Order, error)
}

// WithParticipantActions composes existing pickup/receipt and final-location
// operations without replacing assignment, cancellation or creation services.
func (s *Server) WithParticipantActions(progress ProgressOperations, disposition DispositionOperations) *Server {
	s.progress, s.disposition = progress, disposition
	return s
}

func (s *Server) participantView(ctx context.Context, id string) (*pb.Order, error) {
	caller := shared.Caller(ctx)
	current, err := s.operations.GetOrder(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	return shared.OrderView(ctx, caller, current, s.images)
}

func (s *Server) ConfirmPickup(
	ctx context.Context,
	req *connect.Request[pb.ConfirmPickupRequest],
) (*connect.Response[pb.ConfirmPickupResponse], error) {
	if s.progress == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	o, err := s.progress.ConfirmPickup(ctx, shared.Caller(ctx), req.Msg.GetOrderId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := s.participantView(ctx, o.ID)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ConfirmPickupResponse{Order: view}), nil
}

func (s *Server) ConfirmReceipt(
	ctx context.Context,
	req *connect.Request[pb.ConfirmReceiptRequest],
) (*connect.Response[pb.ConfirmReceiptResponse], error) {
	if s.progress == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	o, err := s.progress.ConfirmReceipt(ctx, shared.Caller(ctx), req.Msg.GetOrderId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := s.participantView(ctx, o.ID)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.ConfirmReceiptResponse{Order: view}), nil
}

func (s *Server) RecordItemLocation(
	ctx context.Context,
	req *connect.Request[pb.RecordItemLocationRequest],
) (*connect.Response[pb.RecordItemLocationResponse], error) {
	if s.disposition == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	o, err := s.disposition.RecordItemLocation(ctx, shared.Caller(ctx), req.Msg.GetOrderId(), req.Msg.GetItemLocation())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := s.participantView(ctx, o.ID)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	return connect.NewResponse(&pb.RecordItemLocationResponse{Order: view}), nil
}
