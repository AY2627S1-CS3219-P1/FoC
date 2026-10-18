package order

import (
	"context"
	"errors"

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

func (s *Server) AcceptOrder(ctx context.Context, req *connect.Request[pb.AcceptOrderRequest]) (*connect.Response[pb.AcceptOrderResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" || caller.Suspended {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.assignments == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	result, err := s.assignments.AcceptOrder(ctx, caller, req.Msg.Id, req.Msg.AttemptId)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, attempt, err := s.assignmentView(ctx, caller, result)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AcceptOrderResponse{Order: view, Attempt: attempt}), nil
}

func (s *Server) AcceptOffer(ctx context.Context, req *connect.Request[pb.AcceptOfferRequest]) (*connect.Response[pb.AcceptOfferResponse], error) {
	caller := shared.Caller(ctx)
	if caller.ID == "" || caller.Suspended {
		return nil, shared.ConnectError(ctx, app.ErrForbidden)
	}
	if s.assignments == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	result, err := s.assignments.AcceptOffer(ctx, caller, req.Msg.Id)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	view, attempt, err := s.assignmentView(ctx, caller, result)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.AcceptOfferResponse{Order: view, Offer: shared.Offer(result.Offer), Attempt: attempt}), nil
}

// A retained attempt grants access to its own outcome, not to the current
// aggregate's private data. Visibility and projection remain F0-owned.
func (s *Server) assignmentView(ctx context.Context, caller app.Caller, result app.AssignmentResult) (*pb.Order, *pb.AssignmentAttemptResult, error) {
	var status pb.AssignmentAttemptStatus
	switch result.Phase {
	case app.AdjustmentPending:
		status = pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_PENDING
	case app.RevertPending:
		status = pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_COMPENSATING
	case app.AssignmentSucceeded:
		status = pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED
	case app.AssignmentRejected:
		status = pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_REJECTED
	default:
		return nil, nil, shared.ConnectError(ctx, app.ErrInvalid)
	}
	attempt := &pb.AssignmentAttemptResult{AttemptId: result.AttemptID, Status: status, Rejection: result.Rejection}
	if result.Phase == app.AssignmentRejected {
		rejection := connect.NewError(connect.CodeFailedPrecondition, errors.New("acceptance rejected"))
		detail, err := connect.NewErrorDetail(attempt)
		if err != nil {
			return nil, nil, shared.ConnectError(ctx, err)
		}
		rejection.AddDetail(detail)
		return nil, nil, rejection
	}
	current, err := s.operations.GetOrder(ctx, caller, result.Order.ID)
	if errors.Is(err, app.ErrNotFound) {
		return nil, attempt, nil
	}
	if err != nil {
		return nil, nil, shared.ConnectError(ctx, err)
	}
	view, err := shared.OrderView(ctx, caller, current, s.images)
	if err != nil {
		return nil, nil, shared.ConnectError(ctx, err)
	}
	return view, attempt, nil
}
