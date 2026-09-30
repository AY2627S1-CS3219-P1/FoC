package workflows_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	handler "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/handlers/workflows"
	w "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/workflows"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type applicationStub struct {
	handler.Operations
	err      error
	proposal w.Proposal
}

func (s *applicationStub) SubmitRequest(_ context.Context, c w.Caller, in w.SubmitRequest) (w.AdditionRequest, error) {
	s.proposal = in.Proposal
	return w.AdditionRequest{ID: "b78b6aa8-510f-4e90-b77a-724cfb880e07", Proposal: in.Proposal, SubmittedBy: c.ID, Status: w.Pending, Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}, s.err
}
func (s *applicationStub) CreateDisablement(_ context.Context, c w.Caller, in w.CreateDisablement) (w.Disablement, error) {
	return w.Disablement{ID: "b78b6aa8-510f-4e90-b77a-724cfb880e07", LocationID: in.LocationID, StartsAt: time.Now(), Reason: in.Reason, CreatedBy: c.ID, Revision: 1, CreatedAt: time.Now(), UpdatedAt: time.Now()}, s.err
}
func (s *applicationStub) UpdateRequest(_ context.Context, c w.Caller, in w.UpdateRequest) (w.AdditionRequest, error) {
	return w.AdditionRequest{}, s.err
}
func fastRequest[T any](m *T, role string) *connect.Request[T] {
	r := connect.NewRequest(m)
	r.Header().Set("X-Test-Role", role)
	return r
}

type fastLocationClients struct {
	rpc.LocationDiscoveryServiceClient
	rpc.LocationAdminServiceClient
	rpc.LocationDisablementServiceClient
}

func fastClients(t *testing.T, s *applicationStub) (fastLocationClients, rpc.LocationAdditionRequestServiceClient) {
	t.Helper()
	principal := func(ctx context.Context) w.Caller { c, _ := ctx.Value(fastPrincipalKey{}).(w.Caller); return c }
	h := handler.New(s, principal, time.Now)
	mux := http.NewServeMux()
	auth := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			role := r.Header.Get("X-Test-Role")
			c := w.Caller{}
			if role != "" {
				c = w.Caller{ID: "test-caller", Role: role}
			}
			next.ServeHTTP(rw, r.WithContext(context.WithValue(r.Context(), fastPrincipalKey{}, c)))
		})
	}
	if e := handler.Mount(mux, h, auth); e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return fastLocationClients{rpc.NewLocationDiscoveryServiceClient(server.Client(), server.URL), rpc.NewLocationAdminServiceClient(server.Client(), server.URL), rpc.NewLocationDisablementServiceClient(server.Client(), server.URL)}, rpc.NewLocationAdditionRequestServiceClient(server.Client(), server.URL)
}

type fastPrincipalKey struct{}

func fastProposal() *pb.LocationInput {
	v := true
	return &pb.LocationInput{Name: "Cafe", IsSupplier: &v, CategoryIds: []string{"63dc656d-4e7b-43ad-a0ba-325b44132766"}, BuildingId: "9dbda827-5a46-4089-9209-a2bde43f18a0", Coordinates: &pb.Coordinates{Latitude: 1.294, Longitude: 103.774}, OpensAt: &timeofday.TimeOfDay{Hours: 22, Minutes: 5}, ClosesAt: &timeofday.TimeOfDay{Hours: 2}}
}
func TestHandlerTranslationAndSafeErrorMapping(t *testing.T) {
	s := &applicationStub{}
	_, client := fastClients(t, s)
	ctx := context.Background()
	input := &pb.SubmitLocationAdditionRequestRequest{Proposal: fastProposal(), IdempotencyKey: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}
	result, e := client.SubmitLocationAdditionRequest(ctx, fastRequest(input, "user"))
	if e != nil {
		t.Fatal(e)
	}
	if s.proposal.OpenFrom == nil || *s.proposal.OpenFrom != 79500000000 || result.Msg.Request.SubmittedBy != "test-caller" || result.Msg.Request.Proposal.OpensAt.Minutes != 5 {
		t.Fatalf("translation: %+v %+v", s.proposal, result.Msg)
	}
	for _, row := range []struct {
		err  error
		code connect.Code
	}{{w.ErrInvalidArgument, connect.CodeInvalidArgument}, {w.ErrNotFound, connect.CodeNotFound}, {w.ErrFailedPrecondition, connect.CodeFailedPrecondition}, {w.ErrAlreadyExists, connect.CodeAlreadyExists}, {w.ErrAborted, connect.CodeAborted}, {w.ErrPermissionDenied, connect.CodePermissionDenied}, {w.ErrUnauthenticated, connect.CodeUnauthenticated}, {errors.New("database password secret"), connect.CodeInternal}} {
		s.err = row.err
		_, e = client.SubmitLocationAdditionRequest(ctx, fastRequest(input, "user"))
		if connect.CodeOf(e) != row.code || strings.Contains(e.Error(), "secret") {
			t.Fatalf("error mapping: %v, expected %v", e, row.code)
		}
	}
}
func TestHandlerAuthorizationAndPresenceValidation(t *testing.T) {
	s := &applicationStub{}
	locations, requests := fastClients(t, s)
	ctx := context.Background()
	for _, role := range []string{"", "user", "suspended_user", "admin", "super_admin"} {
		_, e := locations.CreateDisablement(ctx, fastRequest(&pb.CreateDisablementRequest{LocationId: "8f383cca-1609-4326-8440-f75d943dc924", Reason: "closure", IdempotencyKey: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}, role))
		want := connect.CodeUnknown
		if role == "" {
			want = connect.CodeUnauthenticated
		} else if role == "user" || role == "suspended_user" {
			want = connect.CodePermissionDenied
		}
		if want == connect.CodeUnknown {
			if e != nil {
				t.Fatal(e)
			}
		} else if connect.CodeOf(e) != want {
			t.Fatalf("role %s: %v", role, e)
		}
		_, e = requests.SubmitLocationAdditionRequest(ctx, fastRequest(&pb.SubmitLocationAdditionRequestRequest{Proposal: fastProposal(), IdempotencyKey: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}, role))
		want = connect.CodeUnknown
		if role == "" {
			want = connect.CodeUnauthenticated
		} else if role == "suspended_user" {
			want = connect.CodePermissionDenied
		}
		if want == connect.CodeUnknown {
			if e != nil {
				t.Fatal(e)
			}
		} else if connect.CodeOf(e) != want {
			t.Fatalf("submit role %s: %v", role, e)
		}
	}
	for _, path := range []string{"coordinates", "is_supplier"} {
		_, e := requests.UpdateLocationAdditionRequest(ctx, fastRequest(&pb.UpdateLocationAdditionRequestRequest{Id: "b78b6aa8-510f-4e90-b77a-724cfb880e07", Proposal: &pb.LocationInput{}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{path}}, ExpectedRevision: 1}, "user"))
		if connect.CodeOf(e) != connect.CodeInvalidArgument {
			t.Fatalf("absent masked %s: %v", path, e)
		}
	}
}
func TestMountRequiresAuthenticationBoundary(t *testing.T) {
	s := &applicationStub{}
	h := handler.New(s, func(context.Context) w.Caller { return w.Caller{} }, time.Now)
	if e := handler.Mount(http.NewServeMux(), h, nil); e == nil {
		t.Fatal("mounted without authentication")
	}
}

func TestHandlerLengthLimitsApplyAfterTrimming(t *testing.T) {
	s := &applicationStub{}
	locations, requests := fastClients(t, s)
	ctx := context.Background()
	p := fastProposal()
	p.Name = " " + strings.Repeat("x", 200) + " "
	floor := " " + strings.Repeat("x", 50) + " "
	contact := " " + strings.Repeat("x", 500) + " "
	p.Floor = &floor
	p.Contact = &contact
	p.Details = " " + strings.Repeat("x", 2000) + " "
	_, e := requests.SubmitLocationAdditionRequest(ctx, fastRequest(&pb.SubmitLocationAdditionRequestRequest{Proposal: p, IdempotencyKey: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}, "user"))
	if e != nil {
		t.Fatalf("valid trimmed proposal: %v", e)
	}
	_, e = locations.CreateDisablement(ctx, fastRequest(&pb.CreateDisablementRequest{LocationId: "8f383cca-1609-4326-8440-f75d943dc924", Reason: " " + strings.Repeat("x", 500) + " ", IdempotencyKey: "1e3f8ee1-1362-4e73-bd7f-2c4b61600b29"}, "admin"))
	if e != nil {
		t.Fatalf("valid trimmed reason: %v", e)
	}
}
