package router_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/router"
	accountrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/account"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/health"
	reservationrpc "github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

const (
	orderID     = "11111111-1111-4111-8111-111111111111"
	requesterID = "22222222-2222-4222-8222-222222222222"
)

// Fakes of the handlers' interfaces

type fakeReservations struct {
	calls  []string
	reason reservation.ReleaseReason
	err    error
}

func (f *fakeReservations) record(call string) (reservation.Reservation, error) {
	f.calls = append(f.calls, call)
	r := reservation.Reservation{
		OrderID:        orderID,
		RequesterID:    requesterID,
		Status:         reservation.Held,
		OriginalAmount: 20,
		ReservedAmount: 20,
		Revision:       1,
	}
	return r, f.err
}

func (f *fakeReservations) Reserve(_ context.Context, order, requester string, _ int64) (reservation.Reservation, error) {
	return f.record("reserve " + order + " " + requester)
}

func (f *fakeReservations) AdjustReservation(_ context.Context, _, offer, _ string, _ int64) (reservation.Reservation, error) {
	return f.record("adjust " + offer)
}

func (f *fakeReservations) RevertAdjustment(_ context.Context, _, offer string) (reservation.Reservation, error) {
	return f.record("revert " + offer)
}

func (f *fakeReservations) ReleaseReservation(_ context.Context, order string, reason reservation.ReleaseReason) (reservation.Reservation, error) {
	f.reason = reason
	return f.record("release " + order)
}

func (f *fakeReservations) GetReservation(_ context.Context, order string) (reservation.Reservation, error) {
	return f.record("get " + order)
}

type fakeAccounts struct {
	users []string
	page  account.Page
	err   error
}

func (f *fakeAccounts) GetAccount(_ context.Context, userID string) (account.Account, error) {
	f.users = append(f.users, userID)
	return account.Account{UserID: userID, Balance: 100, Reserved: 20}, f.err
}

func (f *fakeAccounts) ListLedgerEntries(_ context.Context, userID string, p account.Page) (account.LedgerPage, error) {
	f.users, f.page = append(f.users, userID), p
	return account.LedgerPage{PageInfo: account.PageInfo{Page: 1, PageSize: 20}}, f.err
}

type pingable struct{ err error }

func (p pingable) Ping(context.Context) error { return p.err }

// Servers

type servers struct {
	public, internal *httptest.Server
	reservations     *fakeReservations
	accounts         *fakeAccounts
}

func start(t *testing.T) servers {
	t.Helper()
	s := servers{reservations: &fakeReservations{}, accounts: &fakeAccounts{}}
	services := router.RPCServices{
		Health:      &healthrpc.Checker{DB: pingable{}, Services: []string{creditv1connect.CreditReservationServiceName}},
		Account:     &accountrpc.Server{Accounts: s.accounts},
		Reservation: &reservationrpc.Server{Reservations: s.reservations},
	}

	// A bearer token "user:<id>" authenticates as that user.
	authenticate := httpauth.AuthenticateLocal(func(token string) (auth.Caller, error) {
		id, ok := strings.CutPrefix(token, "user:")
		if !ok {
			return auth.Caller{}, errors.New("invalid token")
		}
		return auth.Caller{ID: id}, nil
	})

	s.public = httptest.NewServer(router.Public(services, authenticate))
	s.internal = httptest.NewServer(router.Internal(services))
	t.Cleanup(s.public.Close)
	t.Cleanup(s.internal.Close)
	return s
}

func withBearer[T any](msg *T, token string) *connect.Request[T] {
	req := connect.NewRequest(msg)
	req.Header().Set("Authorization", "Bearer "+token)
	return req
}

// Reservation RPCs

func TestReservationRPCsServeOrderOnTheInternalListener(t *testing.T) {
	s := start(t)
	client := creditv1connect.NewCreditReservationServiceClient(http.DefaultClient, s.internal.URL)
	ctx := context.Background()

	res, err := client.Reserve(ctx, connect.NewRequest(&creditv1.ReserveRequest{RequestId: orderID, RequesterId: requesterID, Amount: 20}))
	if err != nil {
		t.Fatal(err)
	}
	if r := res.Msg.GetReservation(); r.GetRequestId() != orderID || r.GetStatus() != creditv1.ReservationStatus_RESERVATION_STATUS_HELD {
		t.Fatalf("reservation: %v", r)
	}

	_, err = client.ReleaseReservation(ctx, connect.NewRequest(&creditv1.ReleaseReservationRequest{
		RequestId: orderID,
		Reason:    creditv1.ReleaseReason_RELEASE_REASON_EXPIRED,
	}))
	if err != nil || s.reservations.reason != reservation.Expired {
		t.Fatalf("release: %v, reason %q", err, s.reservations.reason)
	}

	want := []string{"reserve " + orderID + " " + requesterID, "release " + orderID}
	if strings.Join(s.reservations.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v", s.reservations.calls)
	}
}

func TestReservationRPCsValidateBeforeCallingTheDomain(t *testing.T) {
	s := start(t)
	client := creditv1connect.NewCreditReservationServiceClient(http.DefaultClient, s.internal.URL)
	ctx := context.Background()

	_, err := client.Reserve(ctx, connect.NewRequest(&creditv1.ReserveRequest{RequestId: "nope", RequesterId: requesterID, Amount: 20}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("bad id: %v", err)
	}
	_, err = client.ReleaseReservation(ctx, connect.NewRequest(&creditv1.ReleaseReservationRequest{RequestId: orderID}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("unspecified reason: %v", err)
	}
	if len(s.reservations.calls) != 0 {
		t.Fatalf("domain called: %v", s.reservations.calls)
	}
}

func TestReservationRPCsReportContractReasons(t *testing.T) {
	s := start(t)
	s.reservations.err = reservation.ErrInsufficientCredits
	client := creditv1connect.NewCreditReservationServiceClient(http.DefaultClient, s.internal.URL)

	_, err := client.Reserve(context.Background(), connect.NewRequest(&creditv1.ReserveRequest{RequestId: orderID, RequesterId: requesterID, Amount: 20}))

	var connectErr *connect.Error
	if !errors.As(err, &connectErr) || connectErr.Code() != connect.CodeFailedPrecondition {
		t.Fatalf("code: %v", err)
	}
	for _, detail := range connectErr.Details() {
		value, _ := detail.Value()
		if info, ok := value.(*errdetails.ErrorInfo); ok && info.Domain == "credit.v1" && info.Reason == "INSUFFICIENT_CREDITS" {
			return
		}
	}
	t.Fatalf("missing INSUFFICIENT_CREDITS reason: %v", connectErr.Details())
}

func TestReservationRPCsAreNotPublic(t *testing.T) {
	s := start(t)
	client := creditv1connect.NewCreditReservationServiceClient(http.DefaultClient, s.public.URL)

	_, err := client.GetReservation(context.Background(), withBearer(&creditv1.GetReservationRequest{RequestId: orderID}, "user:"+requesterID))
	if connect.CodeOf(err) != connect.CodeUnimplemented || len(s.reservations.calls) != 0 {
		t.Fatalf("public reservation RPC: %v, calls %v", err, s.reservations.calls)
	}
}

// Account RPCs

func TestAccountRPCsReadTheCallersAccount(t *testing.T) {
	s := start(t)
	client := creditv1connect.NewCreditAccountServiceClient(http.DefaultClient, s.public.URL)
	ctx := context.Background()

	res, err := client.GetMyAccount(ctx, withBearer(&creditv1.GetMyAccountRequest{}, "user:"+requesterID))
	if err != nil {
		t.Fatal(err)
	}
	if a := res.Msg.GetAccount(); a.GetUserId() != requesterID || a.GetAvailable() != 80 {
		t.Fatalf("account: %v", a)
	}

	_, err = client.ListMyLedgerEntries(ctx, withBearer(&creditv1.ListMyLedgerEntriesRequest{Page: 2, PageSize: 10}, "user:"+requesterID))
	if err != nil || s.accounts.page != (account.Page{Number: 2, Size: 10}) {
		t.Fatalf("list: %v, page %+v", err, s.accounts.page)
	}
	if len(s.accounts.users) != 2 || s.accounts.users[1] != requesterID {
		t.Fatalf("users %v", s.accounts.users)
	}
}

func TestAccountRPCsRejectAnonymousAndInvalidRequests(t *testing.T) {
	s := start(t)
	client := creditv1connect.NewCreditAccountServiceClient(http.DefaultClient, s.public.URL)
	ctx := context.Background()

	if _, err := client.GetMyAccount(ctx, connect.NewRequest(&creditv1.GetMyAccountRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous: %v", err)
	}
	_, err := client.ListMyLedgerEntries(ctx, withBearer(&creditv1.ListMyLedgerEntriesRequest{PageSize: 101}, "user:"+requesterID))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("page size: %v", err)
	}
	if len(s.accounts.users) != 0 {
		t.Fatalf("domain called for %v", s.accounts.users)
	}

	s.accounts.err = account.ErrAccountNotFound
	if _, err := client.GetMyAccount(ctx, withBearer(&creditv1.GetMyAccountRequest{}, "user:"+requesterID)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("missing account: %v", err)
	}
}

// Health

func TestHealthIsServedOnBothListeners(t *testing.T) {
	s := start(t)
	for _, url := range []string{s.public.URL, s.internal.URL} {
		client := grpchealth.NewClient(http.DefaultClient, url)

		res, err := client.Check(context.Background(), &grpchealth.CheckRequest{})
		if err != nil || res.Status != grpchealth.StatusServing {
			t.Fatalf("%s: %v %v", url, res, err)
		}
		_, err = client.Check(context.Background(), &grpchealth.CheckRequest{Service: "other.v1.Service"})
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("%s unknown service: %v", url, err)
		}
	}
}

func TestHealthReportsAnUnreachableDatabase(t *testing.T) {
	checker := &healthrpc.Checker{DB: pingable{err: errors.New("down")}}

	res, err := checker.Check(context.Background(), &grpchealth.CheckRequest{})
	if err != nil || res.Status != grpchealth.StatusNotServing {
		t.Fatalf("%v %v", res, err)
	}
}
