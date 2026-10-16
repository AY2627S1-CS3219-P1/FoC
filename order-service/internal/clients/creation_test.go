package clients

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1/creditv1connect"
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

type creditProbe struct {
	creditv1connect.UnimplementedCreditReservationServiceHandler
	mu      sync.Mutex
	bearers []string
}

func (p *creditProbe) GetReservation(_ context.Context, req *connect.Request[pb.GetReservationRequest]) (*connect.Response[pb.GetReservationResponse], error) {
	p.mu.Lock()
	p.bearers = append(p.bearers, req.Header().Get("Authorization"))
	p.mu.Unlock()
	return connect.NewResponse(&pb.GetReservationResponse{Reservation: &pb.Reservation{OrderId: req.Msg.OrderId, Status: pb.ReservationStatus_RESERVATION_STATUS_RELEASED}}), nil
}

func TestCreditReadsRotatedServiceCredential(t *testing.T) {
	probe := new(creditProbe)
	path, handler := creditv1connect.NewCreditReservationServiceHandler(probe)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	defer server.Close()
	file := filepath.Join(t.TempDir(), "credit-token")
	client := &Credit{Client: creditv1connect.NewCreditReservationServiceClient(server.Client(), server.URL), TokenFile: file}
	for _, token := range []string{"first-service-token", "rotated-service-token"} {
		replacement := file + ".next"
		if err := os.WriteFile(replacement, []byte(token+"\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(replacement, file); err != nil {
			t.Fatal(err)
		}
		ctx := WithUserBearer(context.Background(), "Bearer private-user-token")
		if _, err := client.GetReservation(ctx, "order"); err != nil {
			t.Fatal(err)
		}
	}
	probe.mu.Lock()
	defer probe.mu.Unlock()
	if len(probe.bearers) != 2 || probe.bearers[0] != "Bearer first-service-token" || probe.bearers[1] != "Bearer rotated-service-token" {
		t.Fatalf("bearers=%v", probe.bearers)
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
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte("service-token"), 0600); err != nil {
		t.Fatal(err)
	}
	transport := origin.Client()
	transport.CheckRedirect = NoRedirect
	client := Credit{Client: creditv1connect.NewCreditReservationServiceClient(transport, origin.URL), TokenFile: file}
	if _, err := client.GetReservation(context.Background(), "order"); err == nil {
		t.Fatal("redirect unexpectedly succeeded")
	}
	if reached {
		t.Fatal("credential-bearing request followed redirect")
	}
}

func TestServiceCredentialValidation(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	for _, value := range []string{"", " \n", "two tokens", "null\x00byte", string(make([]byte, 8193))} {
		if err := os.WriteFile(file, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadServiceToken(file); !errors.Is(err, app.ErrUnavailable) {
			t.Fatalf("invalid credential accepted")
		}
	}
	if _, err := ReadServiceToken(file + "-missing"); !errors.Is(err, app.ErrUnavailable) {
		t.Fatal("missing credential accepted")
	}
}
