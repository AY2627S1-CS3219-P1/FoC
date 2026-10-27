//go:build integration

package order_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpcorder "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type loseFirstCancellation struct {
	base http.RoundTripper
	lost atomic.Bool
}

func (r *loseFirstCancellation) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := r.base.RoundTrip(req)
	if err == nil && req.URL.Path == orderv1connect.OrderServiceCancelOrderProcedure &&
		r.lost.CompareAndSwap(false, true) {
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		return nil, errors.New("scripted cancellation response loss after commit")
	}
	return response, err
}

func TestCancellationThroughConnectAndPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := acceptancePostgres(t, ctx)
	repo := app.NewPostgresRepository(pool)
	base := time.Now().UTC().Truncate(time.Microsecond)
	requester, courier, other := uuid.NewString(), uuid.NewString(), uuid.NewString()
	o := seedAcceptanceOrder(t, ctx, repo, requester, base)
	clock := atomic.Int64{}
	clock.Store(base.Add(5 * time.Second).UnixNano())
	now := func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	credit := &mountedAcceptanceCredit{
		views: map[string]app.AssignmentReservation{
			o.ID: {OrderID: o.ID, RequesterID: requester, OriginalAmount: 20, ReservedAmount: 20, State: "held"},
		},
		applied:   map[string]bool{},
		reverted:  map[string]bool{},
		available: 100,
	}
	assigner := app.NewAssigner(repo, credit, app.AssignmentOptions{Now: now})
	canceller := app.NewCanceller(repo, credit, app.AssignmentOptions{Now: now})
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(
				w,
				r.WithContext(
					auth.WithCaller(
						r.Context(),
						auth.Caller{ID: r.Header.Get("X-Test-User"), Role: r.Header.Get("X-Test-Role")},
					),
				),
			)
		})
	}
	services := router.RPCServices{
		Health: &orderv1connect.UnimplementedHealthServiceHandler{},
		Order: rpcorder.NewServerWithCancellation(app.NewService(repo), nil, nil, assigner, canceller).
			WithParticipantActions(app.NewProgress(app.NewMutator(repo)), app.NewDisposition(app.NewMutator(repo))),
		Admin: &orderv1connect.UnimplementedOrderAdminServiceHandler{},
	}
	server := httptest.NewServer(router.Setup(services, authenticate))
	t.Cleanup(server.Close)
	client := orderv1connect.NewOrderServiceClient(server.Client(), server.URL)
	call := func(message *pb.CancelOrderRequest, caller, role string) (*connect.Response[pb.CancelOrderResponse], error) {
		if message.ExpectedRevision == 0 {
			current, err := repo.Get(ctx, message.Id)
			if err == nil {
				message.ExpectedRevision = current.Revision
			} else {
				message.ExpectedRevision = 1
			}
		}
		return client.CancelOrder(ctx, acceptanceRequest(message, caller, role))
	}
	for _, bad := range []*pb.CancelOrderRequest{
		{Id: "bad"}, {Id: o.ID, IdempotencyKey: proto.String("")}, {Id: o.ID, IdempotencyKey: proto.String("bad")}, {Id: o.ID, Reason: proto.String(strings.Repeat("界", 501))},
	} {
		if _, err := call(bad, requester, "user"); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("bad request %v: %v", bad, err)
		}
	}
	for _, role := range []string{"user", "admin", "suspended_user"} {
		if _, err := call(
			&pb.CancelOrderRequest{Id: o.ID},
			other,
			role,
		); connect.CodeOf(
			err,
		) != connect.CodePermissionDenied {
			t.Fatalf("foreign %s: %v", role, err)
		}
	}
	assigned, err := client.AcceptOrder(
		ctx,
		acceptanceRequest(&pb.AcceptOrderRequest{Id: o.ID, AttemptId: uuid.NewString()}, courier, "user"),
	)
	if err != nil || assigned.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_AWAITING_PICKUP {
		t.Fatalf("assign %v %v", assigned, err)
	}
	credit.mu.Lock()
	credit.loseObservation = true
	credit.mu.Unlock()
	key := uuid.NewString()
	request := &pb.CancelOrderRequest{
		Id:               o.ID,
		Reason:           proto.String("  leave  "),
		IdempotencyKey:   &key,
		ExpectedRevision: assigned.Msg.Order.Revision,
	}
	lostClient := orderv1connect.NewOrderServiceClient(
		&http.Client{Transport: &loseFirstCancellation{base: server.Client().Transport}},
		server.URL,
	)
	if _, err := lostClient.CancelOrder(ctx, acceptanceRequest(request, courier, "suspended_user")); err == nil {
		t.Fatal("response loss missing")
	}
	pending, err := call(request, courier, "suspended_user")
	if err != nil || pending.Msg.Cancellation.Status != pb.CancellationStatus_CANCELLATION_STATUS_PENDING ||
		!pending.Msg.Order.CancellationPending ||
		pending.Msg.Order.Delivery == nil {
		t.Fatalf("pending %v %v", pending, err)
	}
	if _, err := client.ConfirmPickup(
		ctx,
		acceptanceRequest(&pb.ConfirmPickupRequest{OrderId: o.ID}, courier, "user"),
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("mounted pickup bypassed pending withdrawal: %v", err)
	}
	id := pending.Msg.Cancellation.OperationId
	read, err := client.GetOrder(ctx, acceptanceRequest(&pb.GetOrderRequest{Id: o.ID}, requester, "suspended_user"))
	if err != nil || !read.Msg.Order.CancellationPending {
		t.Fatalf("pending read %v %v", read, err)
	}
	if _, err := call(
		&pb.CancelOrderRequest{
			Id:               o.ID,
			Reason:           proto.String("changed"),
			IdempotencyKey:   &key,
			ExpectedRevision: assigned.Msg.Order.Revision,
		},
		courier,
		"user",
	); connect.CodeOf(
		err,
	) != connect.CodeAlreadyExists {
		t.Fatalf("changed key %v", err)
	}
	clock.Add(int64(time.Minute))
	if err := app.NewCanceller(repo, credit, app.AssignmentOptions{Now: now}).
		RecoverCancellationsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	recovered, err := call(request, courier, "suspended_user")
	if err != nil || recovered.Msg.Cancellation.OperationId != id ||
		recovered.Msg.Cancellation.Status != pb.CancellationStatus_CANCELLATION_STATUS_SUCCEEDED ||
		recovered.Msg.Order.CancellationPending ||
		recovered.Msg.Order.Delivery != nil ||
		recovered.Msg.Order.DeliveryBuildingName != "COM3" {
		t.Fatalf("recovered %v %v", recovered, err)
	}
	replacement, err := client.AcceptOrder(
		ctx,
		acceptanceRequest(&pb.AcceptOrderRequest{Id: o.ID, AttemptId: uuid.NewString()}, other, "user"),
	)
	if err != nil || replacement.Msg.Order.GetCourierId() != other {
		t.Fatalf("replacement %v %v", replacement, err)
	}
	historical, err := call(request, courier, "suspended_user")
	if err != nil || historical.Msg.Cancellation.OperationId != id || historical.Msg.Order.GetCourierId() != other ||
		historical.Msg.Order.Delivery != nil ||
		historical.Msg.Order.ItemImages != nil {
		t.Fatalf("historical redaction %v %v", historical, err)
	}
	if _, err := call(
		&pb.CancelOrderRequest{Id: o.ID},
		courier,
		"user",
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("former rights %v", err)
	}
	terminalKey := uuid.NewString()
	terminal, err := call(&pb.CancelOrderRequest{Id: o.ID, IdempotencyKey: &terminalKey}, requester, "suspended_user")
	if err != nil || terminal.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_CANCELLED ||
		terminal.Msg.Order.Delivery == nil {
		t.Fatalf("requester %v %v", terminal, err)
	}
	if _, err := call(
		&pb.CancelOrderRequest{Id: o.ID},
		requester,
		"user",
	); connect.CodeOf(
		err,
	) != connect.CodeFailedPrecondition {
		t.Fatalf("keyless terminal %v", err)
	}
	terminalReplay, err := call(
		&pb.CancelOrderRequest{
			Id:               o.ID,
			IdempotencyKey:   &terminalKey,
			ExpectedRevision: terminal.Msg.Order.Revision - 1,
		},
		requester,
		"suspended_user",
	)
	if err != nil || terminalReplay.Msg.Cancellation.OperationId != terminal.Msg.Cancellation.OperationId {
		t.Fatalf("terminal replay %v %v", terminalReplay, err)
	}
	// A second order exercises the real mounted pickup/cancel/disposition seams.
	second := seedAcceptanceOrder(t, ctx, repo, requester, base)
	credit.mu.Lock()
	credit.views[second.ID] = app.AssignmentReservation{
		OrderID:        second.ID,
		RequesterID:    requester,
		OriginalAmount: 20,
		ReservedAmount: 20,
		State:          "held",
	}
	credit.mu.Unlock()
	accepted, err := client.AcceptOrder(
		ctx,
		acceptanceRequest(&pb.AcceptOrderRequest{Id: second.ID, AttemptId: uuid.NewString()}, courier, "user"),
	)
	if err != nil {
		t.Fatal(err)
	}
	picked, err := client.ConfirmPickup(
		ctx,
		acceptanceRequest(&pb.ConfirmPickupRequest{OrderId: second.ID}, courier, "user"),
	)
	if err != nil || picked.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_DELIVERING {
		t.Fatalf("mounted pickup: %v %v", picked, err)
	}
	stale := &pb.CancelOrderRequest{
		Id:               second.ID,
		ExpectedRevision: accepted.Msg.Order.Revision,
		IdempotencyKey:   proto.String(uuid.NewString()),
	}
	if _, err := call(stale, requester, "user"); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale mounted confirmation: %v", err)
	}
	failed, err := call(
		&pb.CancelOrderRequest{
			Id:               second.ID,
			ExpectedRevision: picked.Msg.Order.Revision,
			IdempotencyKey:   proto.String(uuid.NewString()),
		},
		courier,
		"user",
	)
	if err != nil || failed.Msg.Order.Status != pb.OrderStatus_ORDER_STATUS_FAILED ||
		failed.Msg.Order.ItemLocation != nil {
		t.Fatalf("postpickup cancel: %v %v", failed, err)
	}
	location := &pb.RecordItemLocationRequest{OrderId: second.ID, ItemLocation: "Library desk"}
	if _, err := client.RecordItemLocation(
		ctx,
		acceptanceRequest(location, requester, "user"),
	); connect.CodeOf(
		err,
	) != connect.CodePermissionDenied {
		t.Fatalf("requester disposition: %v", err)
	}
	firstLocation, err := client.RecordItemLocation(ctx, acceptanceRequest(location, courier, "suspended_user"))
	if err != nil {
		t.Fatal(err)
	}
	repeatLocation, err := client.RecordItemLocation(ctx, acceptanceRequest(location, courier, "user"))
	if err != nil || repeatLocation.Msg.Order.Revision != firstLocation.Msg.Order.Revision {
		t.Fatalf("location retry: %v %v", repeatLocation, err)
	}
	location.ItemLocation = "Residence reception"
	corrected, err := client.RecordItemLocation(ctx, acceptanceRequest(location, courier, "user"))
	if err != nil || corrected.Msg.Order.GetItemLocation() != "Residence reception" ||
		corrected.Msg.Order.Revision != firstLocation.Msg.Order.Revision+1 {
		t.Fatalf("location correction: %v %v", corrected, err)
	}
	var outcomes, warnings int
	if err := pool.QueryRow(ctx, "SELECT count(*) FILTER (WHERE kind='order.outcome'), count(*) FILTER (WHERE kind='order.courier_warning_requested') FROM order_outbox WHERE order_id=$1", second.ID).
		Scan(&outcomes, &warnings); err != nil ||
		outcomes != 1 ||
		warnings != 1 {
		t.Fatalf("duplicate terminal effects %d %d %v", outcomes, warnings, err)
	}
	t.Log(
		"PASS mounted Connect/Postgres: lost withdrawal response, durable recovery, replacement courier privacy, stale cancellation rejection, pickup, terminal warning, location retry and correction",
	)

}
