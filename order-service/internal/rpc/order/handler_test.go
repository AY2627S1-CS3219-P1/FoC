package order_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/router"
	rpcorder "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/rpc/orderadmin"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/order/v1/orderv1connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const orderID = "37dbf670-f32c-4cca-9473-5861c274e789"
const userID = "c55ce32c-2af7-46d1-a088-6a976d3bdbaf"
const locationID = "41dbf670-f32c-4cca-9473-5861c274e789"

type fakeOperations struct {
	called  string
	caller  app.Caller
	id      string
	request app.ListRequest
	status  app.OfferStatus
	err     error
}

func fixture() app.Order {
	now := time.Date(2026, 10, 15, 12, 34, 56, 123, time.UTC)
	return app.Order{ID: orderID, RequesterID: userID, CourierID: proto.String(orderID), Status: app.Delivered,
		Pickup:               app.LocationSnapshot{LocationID: locationID, Name: "Cafe", BuildingName: "COM1", Floor: proto.String("2")},
		Delivery:             app.LocationSnapshot{LocationID: orderID, Name: "Library", BuildingName: "Central"},
		DeliveryInstructions: proto.String("Call outside"), ItemDescription: "Book", RewardCredits: 42,
		DeliveryDuration: 10 * time.Minute, ExpiresAt: now.Add(time.Hour), AssignedAt: &now, PickedUpAt: &now,
		DeliveredAt: &now, CompletedAt: &now, ClosedAt: &now, CloseReason: proto.String("reason"),
		ItemLocation: proto.String("reception"), UnattendedDelivery: true, Revision: 7, CreatedAt: now, UpdatedAt: now}
}

func (f *fakeOperations) GetOrder(_ context.Context, c app.Caller, id string) (app.Order, error) {
	f.called, f.caller, f.id = "get", c, id
	return fixture(), f.err
}

func (f *fakeOperations) page(name string, c app.Caller, r app.ListRequest) (app.OrderPage, error) {
	f.called, f.caller, f.request = name, c, r
	return app.OrderPage{Orders: []app.Order{fixture()}, PageInfo: app.PageInfo{Page: app.Page{Number: 2, Size: 20}, Total: 21}}, f.err
}

func (f *fakeOperations) ListOpenOrders(_ context.Context, c app.Caller, r app.ListRequest) (app.OrderPage, error) {
	return f.page("open", c, r)
}

func (f *fakeOperations) ListMyOrders(_ context.Context, c app.Caller, r app.ListRequest) (app.OrderPage, error) {
	return f.page("mine", c, r)
}

func (f *fakeOperations) ListUserOrders(_ context.Context, c app.Caller, id string, r app.ListRequest) (app.OrderPage, error) {
	f.id = id
	return f.page("user", c, r)
}

func (f *fakeOperations) ListOffers(_ context.Context, c app.Caller, id string, status app.OfferStatus) ([]app.Offer, error) {
	f.called, f.caller, f.id, f.status = "offers", c, id, status
	o := fixture()
	return []app.Offer{{ID: userID, OrderID: id, CourierID: userID, Credits: 50, Status: app.OfferAccepted, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt}}, f.err
}

type clients struct {
	order    orderv1connect.OrderServiceClient
	admin    orderv1connect.OrderAdminServiceClient
	internal orderv1connect.OrderInternalServiceClient
}

func mount(t *testing.T, f *fakeOperations, role string) clients {
	t.Helper()
	// Identity injection isolates method authorization from the HTTP authenticator,
	// which is exercised at the production server boundary.
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if role != "" {
				r = r.WithContext(auth.WithCaller(r.Context(), auth.Caller{ID: userID, Role: role, Admin: role == "admin" || role == "super_admin"}))
			}
			next.ServeHTTP(w, r)
		})
	}
	server := httptest.NewServer(router.Setup(router.RPCServices{
		Health: &orderv1connect.UnimplementedHealthServiceHandler{},
		Order:  rpcorder.NewServer(f, nil, nil),
		Admin:  orderadmin.NewServer(f, nil),
	}, authenticate))
	t.Cleanup(server.Close)
	return clients{order: orderv1connect.NewOrderServiceClient(server.Client(), server.URL), admin: orderv1connect.NewOrderAdminServiceClient(server.Client(), server.URL), internal: orderv1connect.NewOrderInternalServiceClient(server.Client(), server.URL)}
}

func TestReadRPCConversionAndFilters(t *testing.T) {
	f := &fakeOperations{}
	c := mount(t, f, "suspended_user")
	ctx := context.Background()
	got, err := c.order.GetOrder(ctx, connect.NewRequest(&pb.GetOrderRequest{Id: orderID}))
	if err != nil {
		t.Fatal(err)
	}
	o := got.Msg.Order
	if f.called != "get" || f.id != orderID || f.caller.ID != userID || !f.caller.Suspended {
		t.Fatalf("caller/input not forwarded: %+v", f)
	}
	if o.Id != orderID || o.RequesterId != userID || o.GetCourierId() != orderID || o.Status != pb.OrderStatus_ORDER_STATUS_DELIVERED || o.Revision != 7 || len(o.ItemImages) != 0 || o.RewardCredits != 42 {
		t.Fatalf("order conversion: %+v", o)
	}
	if o.Pickup.GetFloor() != "2" || o.Pickup.Name != "Cafe" || o.Pickup.BuildingName != "COM1" || o.Delivery.Location.Name != "Library" || o.Delivery.GetInstructions() != "Call outside" || o.ItemDescription != "Book" {
		t.Fatalf("snapshot conversion: %+v", o)
	}
	if o.DeliveryDuration.AsDuration() != 10*time.Minute || !o.CreatedAt.AsTime().Equal(fixture().CreatedAt) || o.AssignedAt == nil || o.PickedUpAt == nil || o.DeliveredAt == nil || o.CompletedAt == nil || o.ClosedAt == nil || !o.UnattendedDelivery || o.GetItemLocation() != "reception" || o.GetCloseReason() != "reason" {
		t.Fatalf("lifecycle conversion: %+v", o)
	}
	my, err := c.order.ListMyOrders(ctx, connect.NewRequest(&pb.ListMyOrdersRequest{Role: pb.ParticipantRole_PARTICIPANT_ROLE_COURIER, Status: pb.OrderStatus_ORDER_STATUS_COMPLETED, Page: 2, PageSize: 20}))
	if err != nil || f.request.Role != app.CourierRole || f.request.Status != app.Completed || f.request.Page.Number != 2 || my.Msg.PageInfo.TotalItems != 21 || my.Msg.PageInfo.TotalPages != 2 {
		t.Fatalf("mine filters/page: fake=%+v response=%v err=%v", f, my, err)
	}
	_, err = c.order.ListOpenOrders(ctx, connect.NewRequest(&pb.ListOpenOrdersRequest{PickupLocationId: proto.String(locationID), DeliveryBuildingName: "COM3", Page: 3, PageSize: 10}))
	if err != nil || f.called != "open" || f.request.PickupID != locationID || f.request.DeliveryBuildingName != "COM3" || f.request.Page.Number != 3 || f.request.Page.Size != 10 {
		t.Fatalf("open filters: %+v err=%v", f, err)
	}
	offers, err := c.order.ListOffers(ctx, connect.NewRequest(&pb.ListOffersRequest{OrderId: orderID, Status: pb.OfferStatus_OFFER_STATUS_ACCEPTED}))
	if err != nil || f.id != orderID || f.status != app.OfferAccepted || len(offers.Msg.Offers) != 1 || offers.Msg.Offers[0].Status != pb.OfferStatus_OFFER_STATUS_ACCEPTED || offers.Msg.Offers[0].Credits != 50 {
		t.Fatalf("offer conversion: %v err=%v", offers, err)
	}
	admin := mount(t, f, "admin")
	_, err = admin.admin.ListUserOrders(ctx, connect.NewRequest(&pb.ListUserOrdersRequest{UserId: userID, Role: pb.ParticipantRole_PARTICIPANT_ROLE_REQUESTER, Status: pb.OrderStatus_ORDER_STATUS_CANCELLED, PageSize: 10}))
	if err != nil || f.called != "user" || f.id != userID || !f.caller.Admin || f.request.Role != app.RequesterRole || f.request.Status != app.Cancelled || f.request.Page.Size != 10 {
		t.Fatalf("admin filters: %+v err=%v", f, err)
	}
}

// NFR-05.2.3/NFR-05.2.5 (../../../REQUIREMENTS.md#nfr-05-auth): verify method
// authorization before validation. Identity is injected; persisted relationships
// and JWT verification are not exercised by this handler test.
func TestAuthorizationBeforeValidation(t *testing.T) {
	for _, role := range []string{"", "user", "suspended_user", "admin", "super_admin"} {
		t.Run(role, func(t *testing.T) {
			f := &fakeOperations{}
			c := mount(t, f, role)
			_, err := c.admin.ListUserOrders(context.Background(), connect.NewRequest(&pb.ListUserOrdersRequest{}))
			want := connect.CodePermissionDenied
			if role == "" {
				want = connect.CodeUnauthenticated
			}
			if role == "admin" || role == "super_admin" {
				want = connect.CodeInvalidArgument
			}
			if connect.CodeOf(err) != want || f.called != "" {
				t.Fatalf("code=%v called=%q want=%v", connect.CodeOf(err), f.called, want)
			}
			_, err = c.order.GetOrder(context.Background(), connect.NewRequest(&pb.GetOrderRequest{}))
			want = connect.CodeInvalidArgument
			if role == "" {
				want = connect.CodeUnauthenticated
			}
			if connect.CodeOf(err) != want || f.called != "" {
				t.Fatalf("code=%v called=%q want=%v", connect.CodeOf(err), f.called, want)
			}
		})
	}
}

func TestInvalidReadFiltersNeverReachOperations(t *testing.T) {
	f := &fakeOperations{}
	c := mount(t, f, "admin")
	ctx := context.Background()
	calls := []func() error{
		func() error {
			_, e := c.order.ListMyOrders(ctx, connect.NewRequest(&pb.ListMyOrdersRequest{Status: pb.OrderStatus(999)}))
			return e
		},
		func() error {
			_, e := c.order.ListMyOrders(ctx, connect.NewRequest(&pb.ListMyOrdersRequest{Role: pb.ParticipantRole(999)}))
			return e
		},
		func() error {
			_, e := c.order.ListOffers(ctx, connect.NewRequest(&pb.ListOffersRequest{OrderId: orderID, Status: pb.OfferStatus(999)}))
			return e
		},
		func() error {
			_, e := c.order.ListOpenOrders(ctx, connect.NewRequest(&pb.ListOpenOrdersRequest{PageSize: 101}))
			return e
		},
		func() error {
			_, e := c.order.ListMyOrders(ctx, connect.NewRequest(&pb.ListMyOrdersRequest{Page: -1}))
			return e
		},
		func() error {
			_, e := c.order.ListOpenOrders(ctx, connect.NewRequest(&pb.ListOpenOrdersRequest{PickupLocationId: proto.String("invalid")}))
			return e
		},
		func() error {
			_, e := c.admin.ListUserOrders(ctx, connect.NewRequest(&pb.ListUserOrdersRequest{UserId: userID, Role: pb.ParticipantRole(999)}))
			return e
		},
	}
	for _, call := range calls {
		if err := call(); connect.CodeOf(err) != connect.CodeInvalidArgument || f.called != "" {
			t.Fatalf("err=%v called=%q", err, f.called)
		}
	}
}

func TestDomainErrorsAndPrivateDiagnostics(t *testing.T) {
	for _, row := range []struct {
		err     error
		code    connect.Code
		message string
	}{
		{app.ErrNotFound, connect.CodeNotFound, "order not found"},
		{app.ErrForbidden, connect.CodePermissionDenied, "permission denied"},
		{app.ErrInvalid, connect.CodeInvalidArgument, "invalid argument"},
		{app.ErrPrecondition, connect.CodeFailedPrecondition, "ineligible order action"},
		{app.ErrConflict, connect.CodeAborted, "revision conflict"},
		{context.Canceled, connect.CodeCanceled, "context canceled"},
		{context.DeadlineExceeded, connect.CodeDeadlineExceeded, "context deadline exceeded"},
		{errors.New("database password secret"), connect.CodeInternal, "internal error"},
	} {
		t.Run(row.code.String(), func(t *testing.T) {
			c := mount(t, &fakeOperations{err: fmt.Errorf("private dependency details: %w", row.err)}, "user")
			_, err := c.order.GetOrder(context.Background(), connect.NewRequest(&pb.GetOrderRequest{Id: orderID}))
			var ce *connect.Error
			if !errors.As(err, &ce) || ce.Code() != row.code || ce.Message() != row.message || strings.Contains(ce.Message(), "private") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestMutationAndUploadRPCsRemainUnimplemented(t *testing.T) {
	c := mount(t, &fakeOperations{}, "admin")
	ctx := context.Background()
	calls := []struct {
		name string
		call func() error
	}{
		{"create", func() error {
			_, e := c.order.CreateOrder(ctx, connect.NewRequest(&pb.CreateOrderRequest{PickupLocationId: locationID, Delivery: &pb.DeliveryInput{LocationId: orderID}, ItemDescription: "Book", RewardCredits: 10, DeliveryDuration: durationpb.New(10 * time.Minute), ExpiresAt: timestamppb.New(time.Now().Add(time.Hour)), IdempotencyKey: userID}))
			return e
		}},
		{"image upload", func() error {
			_, e := c.order.CreateItemImageUpload(ctx, connect.NewRequest(&pb.CreateItemImageUploadRequest{ContentType: "image/png", SizeBytes: 42}))
			return e
		}},
		{"accept", func() error {
			_, e := c.order.AcceptOrder(ctx, connect.NewRequest(&pb.AcceptOrderRequest{Id: orderID, AttemptId: userID}))
			return e
		}},
		{"make offer", func() error {
			_, e := c.order.MakeOffer(ctx, connect.NewRequest(&pb.MakeOfferRequest{OrderId: orderID, Credits: 10, IdempotencyKey: userID}))
			return e
		}},
		{"accept offer", func() error {
			_, e := c.order.AcceptOffer(ctx, connect.NewRequest(&pb.AcceptOfferRequest{Id: orderID}))
			return e
		}},
		{"pickup", func() error {
			_, e := c.order.ConfirmPickup(ctx, connect.NewRequest(&pb.ConfirmPickupRequest{OrderId: orderID}))
			return e
		}},
		{"proof upload", func() error {
			_, e := c.order.CreateProofUpload(ctx, connect.NewRequest(&pb.CreateProofUploadRequest{OrderId: orderID, UploadId: userID, ContentType: "image/png"}))
			return e
		}},
		{"delivery", func() error {
			_, e := c.order.ConfirmDelivery(ctx, connect.NewRequest(&pb.ConfirmDeliveryRequest{OrderId: orderID, ProofIds: []string{userID}}))
			return e
		}},
		{"receipt", func() error {
			_, e := c.order.ConfirmReceipt(ctx, connect.NewRequest(&pb.ConfirmReceiptRequest{OrderId: orderID}))
			return e
		}},
		{"cancel", func() error {
			_, e := c.order.CancelOrder(ctx, connect.NewRequest(&pb.CancelOrderRequest{Id: orderID}))
			return e
		}},
		{"location", func() error {
			_, e := c.order.RecordItemLocation(ctx, connect.NewRequest(&pb.RecordItemLocationRequest{OrderId: orderID, ItemLocation: "Reception"}))
			return e
		}},
		{"admin cancel", func() error {
			_, e := c.admin.AdminCancelOrder(ctx, connect.NewRequest(&pb.AdminCancelOrderRequest{Id: orderID, Reason: "Suspended"}))
			return e
		}},
		{"unassign", func() error {
			_, e := c.admin.UnassignCourier(ctx, connect.NewRequest(&pb.UnassignCourierRequest{OrderId: orderID, Reason: "Suspended"}))
			return e
		}},
	}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			want := connect.CodeUnimplemented
			if call.name == "create" || call.name == "image upload" || call.name == "make offer" || call.name == "accept" || call.name == "accept offer" || call.name == "cancel" {
				want = connect.CodeUnavailable
			}
			if err := call.call(); connect.CodeOf(err) != want {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
