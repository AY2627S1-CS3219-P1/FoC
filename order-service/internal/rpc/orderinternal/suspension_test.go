package orderinternal_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderinternal"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

type operations struct {
	calls    int
	decision app.AccountSuspensionDecision
	err      error
}

func (o *operations) Apply(_ context.Context, d app.AccountSuspensionDecision) error {
	o.calls++
	o.decision = d
	return o.err
}

// Source: the suspension contract requires private mounting, validated immutable
// decisions, and no acknowledgment on stale revisions. Workload authentication
// is enforced by Istio and separately exercised by the live mesh smoke suite.
func TestSuspensionPrivateRPCContract(t *testing.T) {
	op := &operations{}
	services := router.RPCServices{
		Health:     &orderv1connect.UnimplementedHealthServiceHandler{},
		Order:      &orderv1connect.UnimplementedOrderServiceHandler{},
		Admin:      &orderv1connect.UnimplementedOrderAdminServiceHandler{},
		Suspension: &rpc.Server{Operations: op},
	}
	authenticate := func(h http.Handler) http.Handler { return h }
	server := httptest.NewServer(router.Internal(services, authenticate))
	defer server.Close()
	client := orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL)
	message := &pb.ApplyAccountSuspensionRequest{
		DecisionId: uuid.NewString(),
		UserId:     uuid.NewString(),
		AdminId:    uuid.NewString(),
		Revision:   1,
		Suspended:  true,
		Reason:     "explicit decision",
	}
	for range 2 {
		// No admin token: authorization comes from the workload boundary, not JWTs.
		response, err := client.ApplyAccountSuspension(context.Background(), connect.NewRequest(message))
		if err != nil || response.Msg.DecisionId != message.DecisionId || op.decision.UserID != message.UserId ||
			op.decision.Revision != 1 {
			t.Fatalf("private dispatch: %+v %v", response, err)
		}
	}
	op.err = app.ErrConflict
	if response, err := client.ApplyAccountSuspension(
		context.Background(),
		connect.NewRequest(message),
	); response != nil ||
		connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale decision acknowledged: %+v %v", response, err)
	}
	message.Revision = 0
	if _, err := client.ApplyAccountSuspension(
		context.Background(),
		connect.NewRequest(message),
	); connect.CodeOf(err) != connect.CodeInvalidArgument ||
		op.calls != 3 {
		t.Fatalf("invalid decision reached operation: %v, calls=%d", err, op.calls)
	}
	public := httptest.NewServer(router.Setup(services, authenticate))
	defer public.Close()
	for _, endpoint := range []struct{ base, path string }{
		{public.URL, orderv1connect.OrderInternalServiceApplyAccountSuspensionProcedure},
		{server.URL, orderv1connect.OrderInternalServiceGetOrderParticipantsProcedure},
	} {
		resp, err := server.Client().Post(endpoint.base+endpoint.path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("unexpected exposure %s: HTTP %d", endpoint.path, resp.StatusCode)
		}
	}
}
