package order

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
)

type CancellationOperations interface {
	Cancel(context.Context, app.Caller, string, string, string) (app.CancellationResult, error)
}

func NewServerWithCancellation(operations Operations, creator CreationOperations, images ImageOperations, assignments AssignmentOperations, cancellations CancellationOperations, progress ...ProgressOperations) *Server {
	s := NewServer(operations, creator, images, assignments)
	s.cancellations = cancellations
	if len(progress) > 0 {
		s.progress = progress[0]
	}
	return s
}

func (s *Server) CancelOrder(ctx context.Context, req *connect.Request[pb.CancelOrderRequest]) (*connect.Response[pb.CancelOrderResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.cancellations == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	// Presence matters: an explicitly empty optional UUID is invalid.
	if req.Msg.IdempotencyKey != nil && req.Msg.GetIdempotencyKey() == "" {
		return nil, shared.ConnectError(ctx, app.ErrInvalid)
	}
	result, err := s.cancellations.Cancel(ctx, caller, req.Msg.Id, req.Msg.GetReason(), req.Msg.GetIdempotencyKey())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	current, err := s.operations.GetOrder(ctx, caller, result.Order.ID)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, err := shared.OrderView(ctx, caller, current, s.images)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	statuses := map[app.CancellationStatus]pb.CancellationStatus{
		app.CancellationPending:    pb.CancellationStatus_CANCELLATION_STATUS_PENDING,
		app.CancellationSucceeded:  pb.CancellationStatus_CANCELLATION_STATUS_SUCCEEDED,
		app.CancellationSuperseded: pb.CancellationStatus_CANCELLATION_STATUS_SUPERSEDED,
	}
	status, ok := statuses[result.Status]
	if !ok {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	return connect.NewResponse(&pb.CancelOrderResponse{Order: view, Cancellation: &pb.CancellationResult{OperationId: result.OperationID, Status: status}}), nil
}
