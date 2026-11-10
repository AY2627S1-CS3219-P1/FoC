package order

import (
	"context"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
)

type OperationStatusOperations interface {
	GetAssignmentAttempt(context.Context, app.Caller, string) (app.AssignmentAttempt, error)
	GetCancellation(context.Context, app.Caller, string) (app.Cancellation, error)
}

func (s *Server) WithOperationStatus(status OperationStatusOperations) *Server {
	s.status = status
	return s
}

func (s *Server) GetAssignmentAttempt(
	ctx context.Context,
	req *connect.Request[pb.GetAssignmentAttemptRequest],
) (*connect.Response[pb.GetAssignmentAttemptResponse], error) {
	if s.status == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	a, err := s.status.GetAssignmentAttempt(ctx, shared.Caller(ctx), req.Msg.AttemptId)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	states := map[app.AssignmentPhase]pb.AssignmentAttemptStatus{
		app.AdjustmentPending:   pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_PENDING,
		app.RevertPending:       pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_COMPENSATING,
		app.AssignmentSucceeded: pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED,
		app.AssignmentRejected:  pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_REJECTED,
	}
	state, ok := states[a.Phase]
	if !ok {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	return connect.NewResponse(
		&pb.GetAssignmentAttemptResponse{
			Attempt: &pb.AssignmentAttemptResult{AttemptId: a.ID, Status: state, Rejection: a.Rejection},
		},
	), nil
}

func (s *Server) GetCancellation(
	ctx context.Context,
	req *connect.Request[pb.GetCancellationRequest],
) (*connect.Response[pb.GetCancellationResponse], error) {
	if s.status == nil {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	a, err := s.status.GetCancellation(ctx, shared.Caller(ctx), req.Msg.OperationId)
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}
	states := map[app.CancellationStatus]pb.CancellationStatus{
		app.CancellationPending:    pb.CancellationStatus_CANCELLATION_STATUS_PENDING,
		app.CancellationSucceeded:  pb.CancellationStatus_CANCELLATION_STATUS_SUCCEEDED,
		app.CancellationSuperseded: pb.CancellationStatus_CANCELLATION_STATUS_SUPERSEDED,
	}
	state, ok := states[a.Status]
	if !ok {
		return nil, shared.ConnectError(ctx, app.ErrUnavailable)
	}
	return connect.NewResponse(
		&pb.GetCancellationResponse{Cancellation: &pb.CancellationResult{OperationId: a.ID, Status: state}},
	), nil
}
