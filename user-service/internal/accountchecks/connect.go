// Package accountchecks adapts private outstanding-work RPCs for account administration.
package accountchecks

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

type Order struct {
	Client orderv1connect.OrderInternalServiceClient
}

func (a Order) GetUserOutstandingWork(ctx context.Context, id uuid.UUID) (service.OutstandingWork, error) {
	r, err := a.Client.GetUserOutstandingWork(ctx,
		connect.NewRequest(&orderv1.GetUserOutstandingWorkRequest{UserId: id.String()}))
	if err != nil {
		return service.OutstandingWork{}, err
	}
	if r == nil || r.Msg == nil {
		return service.OutstandingWork{}, errors.New("missing Order outstanding-work response")
	}
	return workResult(r.Msg.HasOutstandingWork, r.Msg.Reasons, orderReasons)
}

type Credit struct {
	Client creditv1connect.CreditInternalServiceClient
}

func (a Credit) GetUserOutstandingWork(ctx context.Context, id uuid.UUID) (service.OutstandingWork, error) {
	r, err := a.Client.GetUserOutstandingWork(ctx,
		connect.NewRequest(&creditv1.GetUserOutstandingWorkRequest{UserId: id.String()}))
	if err != nil {
		return service.OutstandingWork{}, err
	}
	if r == nil || r.Msg == nil {
		return service.OutstandingWork{}, errors.New("missing Credit outstanding-work response")
	}
	return workResult(r.Msg.HasOutstandingWork, r.Msg.Reasons, creditReasons)
}

// Unknown enum values and contradictory responses are unknown work, not clean.
// Only local descriptions reach the admin; remote error strings stay in logs.
func workResult[E comparable](hasWork bool, reasons []E, descriptions map[E]string) (service.OutstandingWork, error) {
	if !hasWork && len(reasons) != 0 {
		return service.OutstandingWork{}, errors.New("inconsistent outstanding-work response")
	}
	out := service.OutstandingWork{HasOutstandingWork: hasWork}
	for _, reason := range reasons {
		description, ok := descriptions[reason]
		if !ok {
			return service.OutstandingWork{}, errors.New("unknown outstanding-work reason")
		}
		out.Reasons = append(out.Reasons, description)
	}
	return out, nil
}

var orderReasons = map[orderv1.OutstandingWorkReason]string{
	orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_NONTERMINAL_ORDER: "Account has an active order or delivery",
	orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_OPEN_OFFER:        "Account has an unresolved offer",
	orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_ADMISSION: "Account has a pending order or assignment",
	orderv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_WORKFLOW:  "Account has pending order recovery",
}

var creditReasons = map[creditv1.OutstandingWorkReason]string{
	creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_RESERVED_BALANCE:       "Account has reserved credits",
	creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_ACTIVE_RESERVATION:     "Account has an active credit reservation",
	creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_UNSETTLED_ASSIGNMENT:   "Account has an unsettled assignment",
	creditv1.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_FINANCIAL_WORK: "Account has unresolved financial work",
}

var _ service.OutstandingWorkReader = Order{}
var _ service.OutstandingWorkReader = Credit{}
