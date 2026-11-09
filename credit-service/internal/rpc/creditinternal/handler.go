// Package creditinternal serves credit.v1.CreditInternalService to other
// services on the internal listener.
package creditinternal

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
)

// Drain lists the read-only outstanding-work check the handler calls.
type Drain interface {
	OutstandingWork(context.Context, string) (account.OutstandingWork, error)
}

// Server answers service-to-service reads.
type Server struct {
	creditv1connect.UnimplementedCreditInternalServiceHandler
	Drain Drain
}

// GetUserOutstandingWork reports whether the user still has outstanding
// financial work. It is read-only, reflects one consistent snapshot and never
// opens an account; callers treat errors and timeouts as unknown, never as
// empty.
func (s *Server) GetUserOutstandingWork(
	ctx context.Context,
	req *connect.Request[creditv1.GetUserOutstandingWorkRequest],
) (*connect.Response[creditv1.GetUserOutstandingWorkResponse], error) {
	if s.Drain == nil {
		// Fail closed: the caller treats every error as unknown outstanding.
		return nil, connect.NewError(connect.CodeInternal, errors.New("drain check unavailable"))
	}

	work, err := s.Drain.OutstandingWork(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	reasons := make([]creditv1.OutstandingWorkReason, 0, len(work.Reasons))
	for _, reason := range work.Reasons {
		reasons = append(reasons, outstandingReasons[reason])
	}
	return connect.NewResponse(&creditv1.GetUserOutstandingWorkResponse{
		HasOutstandingWork: work.HasOutstandingWork(),
		Reasons:            reasons,
	}), nil
}

var outstandingReasons = map[account.OutstandingWorkReason]creditv1.OutstandingWorkReason{
	account.OutstandingReservedBalance:     creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_RESERVED_BALANCE,
	account.OutstandingActiveReservation:   creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_ACTIVE_RESERVATION,
	account.OutstandingUnsettledAssignment: creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_UNSETTLED_ASSIGNMENT,
}

var _ creditv1connect.CreditInternalServiceHandler = (*Server)(nil)

