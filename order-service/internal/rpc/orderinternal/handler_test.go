package orderinternal_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	orderinternal "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderinternal"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
)

const userID = "c55ce32c-2af7-46d1-a088-6a976d3bdbaf"

type fakeDrain struct {
	called string
	id     string
	work   order.OutstandingWork
	err    error
}

func (f *fakeDrain) OutstandingWork(_ context.Context, userID string) (order.OutstandingWork, error) {
	f.called, f.id = "outstanding", userID
	return f.work, f.err
}

func mount(t *testing.T, drain *fakeDrain) orderv1connect.OrderInternalServiceClient {
	t.Helper()
	mux := router.SetupInternal(router.InternalRPCServices{Internal: orderinternal.NewServer(drain)})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL)
}

func TestGetUserOutstandingWorkMapsReasons(t *testing.T) {
	drain := &fakeDrain{work: order.OutstandingWork{Reasons: []order.OutstandingWorkReason{
		order.OutstandingNonterminalOrder,
		order.OutstandingOpenOffer,
		order.OutstandingPendingAdmission,
		order.OutstandingPendingWorkflow,
	}}}
	client := mount(t, drain)

	response, err := client.GetUserOutstandingWork(
		context.Background(),
		connect.NewRequest(&pb.GetUserOutstandingWorkRequest{UserId: userID}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if drain.called != "outstanding" || drain.id != userID {
		t.Fatalf("input not forwarded: %+v", drain)
	}
	want := []pb.OutstandingWorkReason{
		pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_NONTERMINAL_ORDER,
		pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_OPEN_OFFER,
		pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_ADMISSION,
		pb.OutstandingWorkReason_OUTSTANDING_WORK_REASON_PENDING_WORKFLOW,
	}
	if !response.Msg.HasOutstandingWork || len(response.Msg.Reasons) != len(want) {
		t.Fatalf("reasons: %+v", response.Msg)
	}
	for i, reason := range want {
		if response.Msg.Reasons[i] != reason {
			t.Fatalf("reason %d: got %s want %s", i, response.Msg.Reasons[i], reason)
		}
	}
}

func TestGetUserOutstandingWorkEmptyAndErrors(t *testing.T) {
	drain := &fakeDrain{}
	client := mount(t, drain)

	response, err := client.GetUserOutstandingWork(
		context.Background(),
		connect.NewRequest(&pb.GetUserOutstandingWorkRequest{UserId: userID}),
	)
	if err != nil || response.Msg.HasOutstandingWork || len(response.Msg.Reasons) != 0 {
		t.Fatalf("empty result: %+v err=%v", response, err)
	}

	drain.err = errors.New("database down")
	if _, err = client.GetUserOutstandingWork(
		context.Background(),
		connect.NewRequest(&pb.GetUserOutstandingWorkRequest{UserId: userID}),
	); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("dependency errors must surface, never become empty: %v", err)
	}

	drain.err = order.ErrInvalid
	if _, err = client.GetUserOutstandingWork(
		context.Background(),
		connect.NewRequest(&pb.GetUserOutstandingWorkRequest{UserId: "not-a-uuid"}),
	); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid id: %v", err)
	}
}
