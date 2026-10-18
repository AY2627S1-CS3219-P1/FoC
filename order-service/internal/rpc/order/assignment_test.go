package order_test

import (
	"context"
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
