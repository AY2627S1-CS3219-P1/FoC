package order_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpcorder "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"google.golang.org/protobuf/proto"
)

type proposalRPCFixture struct {
	rpcorder.AssignmentOperations
	called       bool
	caller       app.Caller
	orderID, key string
	credits      int64
}

func (f *proposalRPCFixture) MakeOffer(_ context.Context, c app.Caller, id string, credits int64, key string) (app.Offer, error) {
	f.called, f.caller, f.orderID, f.credits, f.key = true, c, id, credits, key
	return app.Offer{ID: locationID, OrderID: id, CourierID: c.ID, Credits: credits, Status: app.OfferOpen}, nil
}

// Source: MakeOffer protobuf validation, R1.6 counteroffers and U6 suspension.
// This exercises generated Connect transport, router validation and RPC mapping,
// not the PostgreSQL/financial implementation.
func TestMakeOfferGeneratedConnectBoundary(t *testing.T) {
	for _, suspended := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "suspended"}[suspended], func(t *testing.T) {
			f := &proposalRPCFixture{}
			role := "user"
			if suspended {
				role = "suspended_user"
			}
			authenticate := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: userID, Role: role})))
				})
			}
			server := httptest.NewServer(router.Setup(router.RPCServices{Health: &orderv1connect.UnimplementedHealthServiceHandler{}, Order: rpcorder.NewServer(&fakeOperations{}, nil, nil, f), Admin: &orderv1connect.UnimplementedOrderAdminServiceHandler{}}, authenticate))
			t.Cleanup(server.Close)
			client := orderv1connect.NewOrderServiceClient(server.Client(), server.URL)
			result, err := client.MakeOffer(context.Background(), connect.NewRequest(&pb.MakeOfferRequest{OrderId: orderID, Credits: 50, IdempotencyKey: locationID}))
			if suspended {
				if connect.CodeOf(err) != connect.CodePermissionDenied || f.called {
					t.Fatalf("suspended offer: called=%v err=%v", f.called, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !f.called || f.caller.ID != userID || f.orderID != orderID || f.key != locationID || f.credits != 50 || result.Msg.Offer.CourierId != userID || result.Msg.Offer.Credits != 50 {
				t.Fatalf("offer mapping: %+v %v", f, result.Msg)
			}
			_, err = client.MakeOffer(context.Background(), connect.NewRequest(&pb.MakeOfferRequest{OrderId: orderID, Credits: 50, IdempotencyKey: "not-a-uuid"}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid key: %v", err)
			}
		})
	}
}

type acceptanceRPCFixture struct {
	result app.AssignmentResult
	calls  int
	key    string
}

func (f *acceptanceRPCFixture) MakeOffer(context.Context, app.Caller, string, int64, string) (app.Offer, error) {
	return app.Offer{}, app.ErrUnavailable
}
func (f *acceptanceRPCFixture) AcceptOrder(_ context.Context, _ app.Caller, _ string, key string) (app.AssignmentResult, error) {
	f.calls++
	f.key = key
	return f.result, nil
}
func (f *acceptanceRPCFixture) AcceptOffer(context.Context, app.Caller, string) (app.AssignmentResult, error) {
	f.calls++
	return f.result, nil
}

// Source: acknowledged B1/F0 outcome contract: failure stays failed_precondition
// with typed details, while pending/compensating cannot be assignment success.
// This is an isolated generated HTTP/Connect unit boundary, not a new DB/E2E run.
func TestAcceptanceTypedOutcomesAcrossConnect(t *testing.T) {
	for _, phase := range []app.AssignmentPhase{app.AdjustmentPending, app.RevertPending, app.AssignmentSucceeded, app.AssignmentRejected} {
		t.Run(string(phase), func(t *testing.T) {
			f := &acceptanceRPCFixture{result: app.AssignmentResult{Order: app.Order{ID: orderID, Status: app.AwaitingPickup}, AttemptID: locationID, Phase: phase, Rejection: "order_unavailable"}}
			reads := &fakeOperations{}
			authenticate := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					next.ServeHTTP(w, r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: userID, Role: "user"})))
				})
			}
			server := httptest.NewServer(router.Setup(router.RPCServices{Health: &orderv1connect.UnimplementedHealthServiceHandler{}, Order: rpcorder.NewServer(reads, nil, nil, f), Admin: &orderv1connect.UnimplementedOrderAdminServiceHandler{}}, authenticate))
			t.Cleanup(server.Close)
			client := orderv1connect.NewOrderServiceClient(server.Client(), server.URL)
			result, err := client.AcceptOrder(context.Background(), connect.NewRequest(&pb.AcceptOrderRequest{Id: orderID, AttemptId: userID}))
			if phase == app.AssignmentRejected {
				if connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("rejection became success: %v", err)
				}
				var ce *connect.Error
				if !errors.As(err, &ce) {
					t.Fatal("missing Connect error")
				}
				found := false
				for _, detail := range ce.Details() {
					value, e := detail.Value()
					if e != nil {
						t.Fatal(e)
					}
					if a, ok := value.(*pb.AssignmentAttemptResult); ok {
						found = a.AttemptId == locationID && a.Status == pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_REJECTED && a.Rejection == "order_unavailable"
					}
				}
				if !found || reads.called != "" {
					t.Fatalf("missing typed rejection or leaked read: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				want := map[app.AssignmentPhase]pb.AssignmentAttemptStatus{app.AdjustmentPending: pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_PENDING, app.RevertPending: pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_COMPENSATING, app.AssignmentSucceeded: pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED}[phase]
				if result.Msg.Attempt.Status != want || result.Msg.Attempt.AttemptId != locationID || result.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_DELIVERED || reads.called != "get" {
					t.Fatalf("did not use historical outcome plus current read: %v", result.Msg)
				}
			}
			if f.key != userID || f.calls != 1 {
				t.Fatalf("retry identity lost: %+v", f)
			}
			_, err = client.AcceptOrder(context.Background(), connect.NewRequest(&pb.AcceptOrderRequest{Id: orderID}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument || f.calls != 1 {
				t.Fatalf("keyless fallback allowed: %v", err)
			}
		})
	}
}

// Source: acknowledged replay privacy: an offer-only/failed-attempt identity is
// not permission to receive the current Order. Historical result survives 404.
func TestAcceptanceReplayWithoutCurrentVisibilityOmitsOrder(t *testing.T) {
	f := &acceptanceRPCFixture{result: app.AssignmentResult{Order: app.Order{ID: orderID, DeliveryInstructions: proto.String("must not leak")}, AttemptID: locationID, Phase: app.AssignmentSucceeded}}
	reads := &fakeOperations{err: app.ErrNotFound}
	server := rpcorder.NewServer(reads, nil, nil, f)
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: userID, Role: "user"})
	result, err := server.AcceptOrder(ctx, connect.NewRequest(&pb.AcceptOrderRequest{Id: orderID, AttemptId: userID}))
	if err != nil || result.Msg.Order != nil || result.Msg.Attempt.Status != pb.AssignmentAttemptStatus_ASSIGNMENT_ATTEMPT_STATUS_SUCCEEDED {
		t.Fatalf("historical attempt leaked current aggregate: %v %v", result, err)
	}
}

// Source: failure to read/project current state must not turn a recorded success
// into an admission rejection that would cause the client to discard its key.
func TestAcceptedOutcomeProjectionFailureRemainsRetryable(t *testing.T) {
	f := &acceptanceRPCFixture{result: app.AssignmentResult{Order: app.Order{ID: orderID}, AttemptID: locationID, Phase: app.AssignmentSucceeded}}
	server := rpcorder.NewServer(&fakeOperations{err: app.ErrForbidden}, nil, nil, f)
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: userID, Role: "user"})
	_, err := server.AcceptOrder(ctx, connect.NewRequest(&pb.AcceptOrderRequest{Id: orderID, AttemptId: userID}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("projection failure was treated as admission rejection: %v", err)
	}
}
