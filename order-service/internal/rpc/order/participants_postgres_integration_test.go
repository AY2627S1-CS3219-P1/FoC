//go:build integration

package order_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	internalrpc "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderinternal"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
)

func TestParticipantLookupThroughConnectAndPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := acceptancePostgres(t, ctx)
	repo := app.NewPostgresRepository(pool)
	requester, former, current := uuid.NewString(), uuid.NewString(), uuid.NewString()
	o := seedAcceptanceOrder(t, ctx, repo, requester, time.Now().UTC())
	services := router.RPCServices{
		Internal: internalrpc.NewServer(app.NewParticipants(repo)),
		Health:   &orderv1connect.UnimplementedHealthServiceHandler{},
		Order:    &orderv1connect.UnimplementedOrderServiceHandler{},
		Admin:    &orderv1connect.UnimplementedOrderAdminServiceHandler{},
	}
	server := httptest.NewServer(router.Internal(services))
	defer server.Close()
	client := orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL)
	lookup := func() *pb.GetOrderParticipantsResponse {
		t.Helper()
		r, e := client.GetOrderParticipants(ctx, connect.NewRequest(&pb.GetOrderParticipantsRequest{OrderId: o.ID}))
		if e != nil {
			t.Fatal(e)
		}
		return r.Msg
	}
	got := lookup()
	if got.GetOrderId() != o.ID || got.GetRequesterId() != requester || got.CourierId != nil ||
		got.GetStatus() != pb.OrderStatus_ORDER_STATUS_AWAITING_COURIER {
		t.Fatalf("unassigned: %v", got)
	}
	// Read the same persisted ID after reassignment, detecting stale/cached responses.
	for _, courier := range []string{former, current} {
		if _, e := pool.Exec(
			ctx,
			"UPDATE orders SET courier_id=$2,status='awaiting_pickup' WHERE id=$1",
			o.ID,
			courier,
		); e != nil {
			t.Fatal(e)
		}
		got = lookup()
		if got.GetCourierId() != courier || got.GetStatus() != pb.OrderStatus_ORDER_STATUS_AWAITING_PICKUP {
			t.Fatalf("current assignment: %v", got)
		}
	}
	if _, e := pool.Exec(ctx, "UPDATE orders SET status='completed' WHERE id=$1", o.ID); e != nil {
		t.Fatal(e)
	}
	got = lookup()
	if got.GetCourierId() != current || got.GetStatus() != pb.OrderStatus_ORDER_STATUS_COMPLETED {
		t.Fatalf("completed: %v", got)
	}
	for _, tc := range []struct {
		id   string
		code connect.Code
	}{{uuid.NewString(), connect.CodeNotFound}, {"bad", connect.CodeInvalidArgument}, {uuid.Nil.String(), connect.CodeInvalidArgument}} {
		_, e := client.GetOrderParticipants(ctx, connect.NewRequest(&pb.GetOrderParticipantsRequest{OrderId: tc.id}))
		if connect.CodeOf(e) != tc.code {
			t.Fatalf("%s: %v", tc.id, e)
		}
	}
	public := httptest.NewServer(router.Setup(services, func(next http.Handler) http.Handler { return next }))
	defer public.Close()
	for _, token := range []string{"", "Bearer forged-admin"} {
		req, e := http.NewRequestWithContext(
			ctx,
			http.MethodPost,
			public.URL+orderv1connect.OrderInternalServiceGetOrderParticipantsProcedure,
			nil,
		)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", token)
		r, e := public.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		_ = r.Body.Close()
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("private route exposed: %d", r.StatusCode)
		}
	}
	pool.Close()
	_, e := client.GetOrderParticipants(ctx, connect.NewRequest(&pb.GetOrderParticipantsRequest{OrderId: o.ID}))
	if connect.CodeOf(e) != connect.CodeInternal {
		t.Fatalf("closed database: %v", e)
	}
}
