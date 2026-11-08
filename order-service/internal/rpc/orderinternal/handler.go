// Package orderinternal serves order.v1.OrderInternalService to other services.
package orderinternal

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/shared"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

// Drain lists the read-only outstanding-work check the handler calls.
type Drain interface {
	OutstandingWork(context.Context, string) (order.OutstandingWork, error)
}

// Server answers service-to-service reads. GetOrderParticipants stays
// unimplemented until Communication consumes it.
type Server struct {
	orderv1connect.UnimplementedOrderInternalServiceHandler
	drain Drain
}

func NewServer(drain Drain) *Server { return &Server{drain: drain} }

// GetUserOutstandingWork reports whether the user still has outstanding work.
// It is read-only and reflects one consistent snapshot; callers treat errors
// and timeouts as unknown, never as empty.
func (s *Server) GetUserOutstandingWork(
	ctx context.Context,
	req *connect.Request[pb.GetUserOutstandingWorkRequest],
) (*connect.Response[pb.GetUserOutstandingWorkResponse], error) {
	if s.drain == nil {
		return nil, shared.ConnectError(ctx, order.ErrUnavailable)
	}

	work, err := s.drain.OutstandingWork(ctx, req.Msg.GetUserId())
	if err != nil {
		return nil, shared.ConnectError(ctx, err)
	}

	reasons := make([]pb.OutstandingWorkReason, 0, len(work.Reasons))
	for _, reason := range work.Reasons {
		reasons = append(reasons, outstandingReasons[reason])
	}
	return connect.NewResponse(&pb.GetUserOutstandingWorkResponse{
		HasOutstandingWork: work.HasOutstandingWork(),
		Reasons:            reasons,
	}), nil
}

var outstandingReasons = map[order.OutstandingWorkReason]pb.OutstandingWorkReason{
	order.OutstandingNonterminalOrder: pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_NONTERMINAL_ORDER,
	order.OutstandingOpenOffer:        pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_OPEN_OFFER,
	order.OutstandingPendingAdmission: pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_ADMISSION,
	order.OutstandingPendingWorkflow:  pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_WORKFLOW,
}

var _ orderv1connect.OrderInternalServiceHandler = (*Server)(nil)
