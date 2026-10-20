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
}

func (o *operations) Apply(_ context.Context, d app.AccountSuspensionDecision) error {
	o.calls++
	o.decision = d
	return nil
}

type fixtureIdentity struct{}

// This identity injection exists only in tests. It is not an implementation of
// #29's production workload authentication or authorization boundary.
func testAuthentication(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), fixtureIdentity{}, r.Header.Get("X-Test-Workload"))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func testAuthorization() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if ctx.Value(fixtureIdentity{}) != "user-service" {
				return nil, connect.NewError(connect.CodePermissionDenied, nil)
			}
			return next(ctx, req)
		}
	})
}

func TestSuspensionRPCRequiresExplicitTrustedCallerBoundary(t *testing.T) {
	op := &operations{}
	server := httptest.NewServer(router.SetupSuspension(&rpc.Server{Operations: op}, testAuthentication, testAuthorization()))
	defer server.Close()
	client := orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL)
	message := &pb.ApplyAccountSuspensionRequest{DecisionId: uuid.NewString(), UserId: uuid.NewString(), AdminId: uuid.NewString(), Revision: 1, Suspended: true, Reason: "explicit decision"}
	for _, identity := range []string{"", "admin", "order-service", "credit-service"} {
		req := connect.NewRequest(message)
		req.Header().Set("X-Test-Workload", identity)
		req.Header().Set("Authorization", "Bearer expired-admin-token")
		if _, err := client.ApplyAccountSuspension(context.Background(), req); connect.CodeOf(err) != connect.CodePermissionDenied || op.calls != 0 {
			t.Fatalf("identity %q: %v calls=%d", identity, err, op.calls)
		}
	}
	for range 2 {
		req := connect.NewRequest(message)
		req.Header().Set("X-Test-Workload", "user-service")
		// No admin token is present: durable dispatch uses the injected workload.
		response, err := client.ApplyAccountSuspension(context.Background(), req)
		if err != nil || response.Msg.DecisionId != message.DecisionId || op.decision.UserID != message.UserId || op.decision.Revision != 1 {
			t.Fatalf("workload dispatch: %+v %v", response, err)
		}
	}
	for _, missing := range []bool{true, false} {
		var authenticate func(http.Handler) http.Handler = testAuthentication
		var authorize connect.Interceptor = testAuthorization()
		if missing {
			authenticate = nil
		} else {
			authorize = nil
		}
		closed := httptest.NewServer(router.SetupSuspension(&rpc.Server{Operations: op}, authenticate, authorize))
		resp, err := closed.Client().Post(closed.URL+orderv1connect.OrderInternalServiceApplyAccountSuspensionProcedure, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		closed.Close()
		if resp.StatusCode != http.StatusServiceUnavailable || op.calls != 2 {
			t.Fatalf("unconfigured boundary: HTTP %d calls=%d", resp.StatusCode, op.calls)
		}
	}
	public := httptest.NewServer(router.Setup(router.RPCServices{Health: &orderv1connect.UnimplementedHealthServiceHandler{}, Order: &orderv1connect.UnimplementedOrderServiceHandler{}, Admin: &orderv1connect.UnimplementedOrderAdminServiceHandler{}}, func(h http.Handler) http.Handler { return h }))
	defer public.Close()
	resp, err := public.Client().Post(public.URL+orderv1connect.OrderInternalServiceApplyAccountSuspensionProcedure, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("internal RPC exposed publicly: HTTP %d", resp.StatusCode)
	}
}
