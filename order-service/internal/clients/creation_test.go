package clients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func TestCreditRefusalRequiresTypedReasonAndCode(t *testing.T) {
	for _, tc := range []struct {
		name, domain, reason string
		code                 connect.Code
		want                 bool
	}{
		{"typed refusal", "credit.v1", "INSUFFICIENT_CREDITS", connect.CodeFailedPrecondition, true},
		{"wrong domain", "order.v1", "INSUFFICIENT_CREDITS", connect.CodeFailedPrecondition, false},
		{"wrong reason", "credit.v1", "IDEMPOTENCY_CONFLICT", connect.CodeFailedPrecondition, false},
		{"wrong code", "credit.v1", "INSUFFICIENT_CREDITS", connect.CodeUnavailable, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := connect.NewError(tc.code, errors.New("do not branch on this message"))
			detail, e := connect.NewErrorDetail(&errdetails.ErrorInfo{Domain: tc.domain, Reason: tc.reason})
			if e != nil {
				t.Fatal(e)
			}
			err.AddDetail(detail)
			if got := hasCreditReason(err, connect.CodeFailedPrecondition, "INSUFFICIENT_CREDITS"); got != tc.want {
				t.Fatalf("got=%v", got)
			}
		})
	}
	if hasCreditReason(errors.New("INSUFFICIENT_CREDITS"), connect.CodeFailedPrecondition, "INSUFFICIENT_CREDITS") {
		t.Fatal("trusted untyped error")
	}
}

func TestCredentialClientDoesNotFollowRedirect(t *testing.T) {
	reached := false
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	transport := origin.Client()
	transport.CheckRedirect = NoRedirect
	client := Credit{
		Client: creditv1connect.NewCreditReservationServiceClient(transport, origin.URL),
	}
	if _, err := client.GetReservation(context.Background(), "order"); err == nil {
		t.Fatal("redirect unexpectedly succeeded")
	}
	if reached {
		t.Fatal("financial request followed redirect")
	}
}

func TestSupplierForwardsVerifiedUserBearer(t *testing.T) {
	const bearer = "Bearer verified-user-token"
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if got := r.Header.Get("Authorization"); got != bearer {
			t.Errorf("authorization=%q", got)
		}
		http.Error(w, "location unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()

	client := Supplier{Client: locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL)}
	if _, err := client.GetLocation(context.Background(), "location"); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("missing credential: %v", err)
	}
	if calls != 0 {
		t.Fatal("sent unauthenticated lookup")
	}

	ctx := auth.WithUserBearer(context.Background(), bearer)
	if _, err := client.GetLocation(ctx, "location"); !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("unavailable supplier: %v", err)
	}
	if calls != 1 {
		t.Fatalf("calls=%d, want 1", calls)
	}
}
