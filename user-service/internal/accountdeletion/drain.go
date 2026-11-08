package accountdeletion

import (
	"context"
	"sync"

	"connectrpc.com/connect"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	creditv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	orderv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	orderv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

// compositeDrainer combines the Order and Credit outstanding-work checks into a
// single port. Either service answering with work or error postpones
// finalization; both must be clean for the account to become eligible. The two
// drains run concurrently so one slow port does not block the other's answer.
type compositeDrainer struct {
	order  Drainer
	credit Drainer
}

// unavailableDrainer postpones every account. It is the fallback drainer used
// until the Order and Credit drains are wired, so an account is never
// finalized before the outstanding-work port can answer.
type unavailableDrainer struct{}

func (unavailableDrainer) GetUserOutstandingWork(context.Context, uuid.UUID) (WorkResult, error) {
	return WorkResult{HasOutstandingWork: true, Reasons: []string{checkUnavailable}, Available: false}, nil
}

func (c compositeDrainer) GetUserOutstandingWork(
	ctx context.Context,
	userID uuid.UUID,
) (WorkResult, error) {
	var (
		wg                  sync.WaitGroup
		orderRes, creditRes WorkResult
		orderErr, creditErr error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		orderRes, orderErr = c.order.GetUserOutstandingWork(ctx, userID)
	}()
	go func() {
		defer wg.Done()
		creditRes, creditErr = c.credit.GetUserOutstandingWork(ctx, userID)
	}()
	wg.Wait()

	reasons := make([]string, 0, len(orderRes.Reasons)+len(creditRes.Reasons))
	reasons = append(reasons, orderRes.Reasons...)
	reasons = append(reasons, creditRes.Reasons...)

	if orderErr != nil || creditErr != nil {
		// An unavailable dependency is unknown work, never clean: postpone.
		return WorkResult{
			HasOutstandingWork: true,
			Reasons:            append(reasons, checkUnavailable),
			Available:          false,
		}, nil
	}
	return WorkResult{
		HasOutstandingWork: orderRes.HasOutstandingWork || creditRes.HasOutstandingWork,
		Reasons:            reasons,
		Available:          true,
	}, nil
}

// orderDrainer adapts the Order internal service client to the worker port.
type orderDrainer struct {
	client orderv1connect.OrderInternalServiceClient
}

func (d orderDrainer) GetUserOutstandingWork(
	ctx context.Context,
	userID uuid.UUID,
) (WorkResult, error) {
	// A single RPC has its own deadline so a hung port reads as "unknown".
	callCtx, cancel := context.WithTimeout(ctx, rpcDeadline)
	defer cancel()
	resp, err := d.client.GetUserOutstandingWork(callCtx,
		connect.NewRequest(&orderv1.GetUserOutstandingWorkRequest{UserId: userID.String()}))
	if err != nil {
		return WorkResult{Available: false}, err
	}
	return toWorkResult(enumNames(resp.Msg.GetReasons()), resp.Msg.GetHasOutstandingWork()), nil
}

// creditDrainer adapts the Credit internal service client to the worker port.
type creditDrainer struct {
	client creditv1connect.CreditInternalServiceClient
}

func (d creditDrainer) GetUserOutstandingWork(
	ctx context.Context,
	userID uuid.UUID,
) (WorkResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, rpcDeadline)
	defer cancel()
	resp, err := d.client.GetUserOutstandingWork(callCtx,
		connect.NewRequest(&creditv1.GetUserOutstandingWorkRequest{UserId: userID.String()}))
	if err != nil {
		return WorkResult{Available: false}, err
	}
	return toWorkResult(enumNames(resp.Msg.GetReasons()), resp.Msg.GetHasOutstandingWork()), nil
}

func toWorkResult(reasons []string, hasWork bool) WorkResult {
	return WorkResult{HasOutstandingWork: hasWork, Reasons: reasons, Available: true}
}

// enumNames renders a repeated protobuf enum to its stable, wire-facing string
// code so reason categories survive service boundaries unchanged. The unnamed
// zero value is dropped so it never surfaces as a waiting reason.
func enumNames[E interface {
	comparable
	String() string
}](reasons []E) []string {
	var zero E
	out := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		if reason != zero {
			out = append(out, reason.String())
		}
	}
	return out
}

// NewCompositeDrainer returns the combined Order and Credit drainer.
func NewCompositeDrainer(
	order orderv1connect.OrderInternalServiceClient,
	credit creditv1connect.CreditInternalServiceClient,
) Drainer {
	return compositeDrainer{order: orderDrainer{client: order}, credit: creditDrainer{client: credit}}
}

// NewUnavailableDrainer returns a drainer that postpones every account. It is
// used until the Order and Credit drains are wired, so an account is never
// finalized before the outstanding-work port can answer.
func NewUnavailableDrainer() Drainer { return unavailableDrainer{} }
