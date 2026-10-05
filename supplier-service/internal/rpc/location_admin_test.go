package rpc

import (
	"context"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	sharedauth "github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"github.com/go-chi/chi/v5"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const buildingID = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"

type fakeAdmin struct {
	called  string
	caller  location.Caller
	created location.CreateRequest
	updated location.UpdateRequest
	err     error
}

func (f *fakeAdmin) Create(ctx context.Context, req location.CreateRequest) (location.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller, f.created = "create", caller, req
	return testAdminLocation(), f.err
}
func (f *fakeAdmin) Update(ctx context.Context, req location.UpdateRequest) (location.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller, f.updated = "update", caller, req
	return testAdminLocation(), f.err
}
func (f *fakeAdmin) Archive(ctx context.Context, _ string) (location.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller = "archive", caller
	return testAdminLocation(), f.err
}
func (f *fakeAdmin) Unarchive(ctx context.Context, _ string) (location.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller = "unarchive", caller
	return testAdminLocation(), f.err
}

func testAdminLocation() location.Location {
	return location.Location{ID: locationID, Name: "Cafe", Building: location.Building{ID: buildingID}, Coordinates: location.Coordinates{Latitude: 1.294, Longitude: 103.774}, Revision: 1}
}

func adminClient(t *testing.T, admin LocationAdmin, role string) locationv1connect.LocationAdminServiceClient {
	t.Helper()
	auth := newTestAuth(t)
	path, handler := locationv1connect.NewLocationAdminServiceHandler(NewLocationAdminServer(admin),
		connect.WithInterceptors(sharedauth.RequireAdmin(), validate.NewInterceptor()))
	router := chi.NewRouter()
	router.Mount(path, auth.authenticator.Authenticate(handler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	if role == "" {
		return locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL)
	}
	return locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL, bearer(auth.token(t, role)))
}

func validCreateRequest() *locationv1.CreateLocationRequest {
	return &locationv1.CreateLocationRequest{IdempotencyKey: locationID, Location: &locationv1.LocationInput{
		Name: "Cafe", IsSupplier: proto.Bool(false), BuildingId: buildingID,
		Coordinates: &locationv1.Coordinates{Latitude: 1.294, Longitude: 103.774},
	}}
}

func TestAdminRPCOnlyAdministratorRoles(t *testing.T) {
	for _, role := range []string{"admin", "super_admin", "user", "suspended_user", ""} {
		t.Run(role, func(t *testing.T) {
			fake := &fakeAdmin{}
			client := adminClient(t, fake, role)
			ctx := context.Background()
			calls := []struct {
				name string
				call func() error
			}{
				{"create", func() error {
					_, err := client.CreateLocation(ctx, connect.NewRequest(validCreateRequest()))
					return err
				}},
				{"update", func() error {
					_, err := client.UpdateLocation(ctx, connect.NewRequest(&locationv1.UpdateLocationRequest{Id: locationID, ExpectedRevision: 1, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}}, Location: &locationv1.LocationInput{Floor: proto.String("2")}}))
					return err
				}},
				{"archive", func() error {
					_, err := client.ArchiveLocation(ctx, connect.NewRequest(&locationv1.ArchiveLocationRequest{Id: locationID}))
					return err
				}},
				{"unarchive", func() error {
					_, err := client.UnarchiveLocation(ctx, connect.NewRequest(&locationv1.UnarchiveLocationRequest{Id: locationID}))
					return err
				}},
			}
			for _, item := range calls {
				t.Run(item.name, func(t *testing.T) {
					fake.called = ""
					err := item.call()
					if role == "admin" || role == "super_admin" {
						if err != nil || fake.called != item.name || fake.caller.ID != "user-1" || !fake.caller.Admin {
							t.Fatalf("call=%q caller=%+v err=%v", fake.called, fake.caller, err)
						}
						return
					}
					want := connect.CodePermissionDenied
					if role == "" {
						want = connect.CodeUnauthenticated
					}
					if connect.CodeOf(err) != want || fake.called != "" {
						t.Fatalf("code=%v call=%q, want %v and no call", connect.CodeOf(err), fake.called, want)
					}
				})
			}
		})
	}
}

func TestAdminAuthorizationPrecedesValidation(t *testing.T) {
	client := adminClient(t, &fakeAdmin{}, "user")
	_, err := client.CreateLocation(context.Background(), connect.NewRequest(&locationv1.CreateLocationRequest{}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code=%v", connect.CodeOf(err))
	}
}

func TestAdminRPCClockPrecisionChecksOnlyMaskedFields(t *testing.T) {
	fake := &fakeAdmin{}
	client := adminClient(t, fake, "admin")
	create := validCreateRequest()
	create.Location.OpensAt = &timeofday.TimeOfDay{Hours: 8, Seconds: 1}
	create.Location.ClosesAt = &timeofday.TimeOfDay{Hours: 18}
	_, err := client.CreateLocation(context.Background(), connect.NewRequest(create))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || fake.called != "" {
		t.Fatalf("create code=%v called=%q", connect.CodeOf(err), fake.called)
	}
	update := &locationv1.UpdateLocationRequest{Id: locationID, ExpectedRevision: 1, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}}, Location: &locationv1.LocationInput{Floor: proto.String("2"), OpensAt: &timeofday.TimeOfDay{Hours: 8, Nanos: 1}}}
	_, err = client.UpdateLocation(context.Background(), connect.NewRequest(update))
	if err != nil || fake.called != "update" || fake.updated.Input.OpensAt == nil {
		t.Fatalf("unmasked time: err=%v called=%q", err, fake.called)
	}
	fake.called = ""
	update.UpdateMask.Paths = []string{"opens_at", "closes_at"}
	_, err = client.UpdateLocation(context.Background(), connect.NewRequest(update))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || fake.called != "" {
		t.Fatalf("masked time: code=%v called=%q", connect.CodeOf(err), fake.called)
	}
}

func TestAdminRPCMapsDomainErrors(t *testing.T) {
	for _, row := range []struct {
		err  error
		code connect.Code
	}{
		{location.ErrInvalidArgument, connect.CodeInvalidArgument},
		{location.ErrFailedPrecondition, connect.CodeFailedPrecondition},
		{location.ErrAlreadyExists, connect.CodeAlreadyExists},
		{location.ErrAborted, connect.CodeAborted},
		{location.ErrNotFound, connect.CodeNotFound},
	} {
		fake := &fakeAdmin{err: row.err}
		client := adminClient(t, fake, "admin")
		_, err := client.ArchiveLocation(context.Background(), connect.NewRequest(&locationv1.ArchiveLocationRequest{Id: locationID}))
		if connect.CodeOf(err) != row.code {
			t.Fatalf("error %v maps to %v, want %v", row.err, connect.CodeOf(err), row.code)
		}
	}
}
