package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/rpcauth"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	sharedauth "github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	location "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	domainshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"github.com/go-chi/chi/v5"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

const buildingID = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"

type fakeAdmin struct {
	called  string
	caller  sharedauth.Caller
	created location.AdminCreateRequest
	updated location.AdminUpdateRequest
	err     error
}

func (f *fakeAdmin) Create(ctx context.Context, req location.AdminCreateRequest) (domainshared.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller, f.created = "create", caller, req
	return testAdminLocation(), f.err
}
func (f *fakeAdmin) Update(ctx context.Context, req location.AdminUpdateRequest) (domainshared.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller, f.updated = "update", caller, req
	return testAdminLocation(), f.err
}
func (f *fakeAdmin) Archive(ctx context.Context, _ string) (domainshared.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller = "archive", caller
	return testAdminLocation(), f.err
}
func (f *fakeAdmin) Unarchive(ctx context.Context, _ string) (domainshared.Location, error) {
	caller, _ := sharedauth.CallerFromContext(ctx)
	f.called, f.caller = "unarchive", caller
	return testAdminLocation(), f.err
}

func testAdminLocation() domainshared.Location {
	return domainshared.Location{ID: locationID, Name: "Cafe", Building: domainshared.Building{ID: buildingID}, Coordinates: domainshared.Coordinates{Latitude: 1.294, Longitude: 103.774}, Revision: 1}
}

func adminClient(t *testing.T, admin LocationAdmin, role string) locationv1connect.LocationAdminServiceClient {
	t.Helper()
	auth := rpcauth.New(t)
	path, handler := locationv1connect.NewLocationAdminServiceHandler(NewLocationAdminServer(admin),
		connect.WithInterceptors(sharedauth.RequireAdmin(), validate.NewInterceptor()))
	router := chi.NewRouter()
	router.Mount(path, auth.Authenticator.Authenticate(handler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	if role == "" {
		return locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL)
	}
	return locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL, rpcauth.Bearer(auth.Token(t, role)))
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
		{location.AdminErrInvalidArgument, connect.CodeInvalidArgument},
		{location.AdminErrFailedPrecondition, connect.CodeFailedPrecondition},
		{location.AdminErrAlreadyExists, connect.CodeAlreadyExists},
		{location.AdminErrAborted, connect.CodeAborted},
		{location.AdminErrNotFound, connect.CodeNotFound},
	} {
		fake := &fakeAdmin{err: row.err}
		client := adminClient(t, fake, "admin")
		_, err := client.ArchiveLocation(context.Background(), connect.NewRequest(&locationv1.ArchiveLocationRequest{Id: locationID}))
		if connect.CodeOf(err) != row.code {
			t.Fatalf("error %v maps to %v, want %v", row.err, connect.CodeOf(err), row.code)
		}
	}
}

const locationID = "c0a3f4c4-12f0-4c17-aa44-8cdf6e76c94b"

func TestAdminRPCPreservesSharedErrorResponses(t *testing.T) {

	for _, row := range []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{"not found", domainshared.ErrNotFound, connect.CodeNotFound, "location not found"},
		{"wrapped not found", fmt.Errorf("database context: %w", domainshared.ErrNotFound), connect.CodeNotFound, "location not found"},
		{"permission denied", domainshared.ErrPermissionDenied, connect.CodePermissionDenied, "permission denied"},
		{"wrapped permission denied", fmt.Errorf("private context: %w", domainshared.ErrPermissionDenied), connect.CodePermissionDenied, "permission denied"},
		{"validation", errs.NewBadRequestError("classification is required"), connect.CodeInvalidArgument, "classification is required"},
		{"wrapped validation", fmt.Errorf("private context: %w", errs.NewBadRequestError("classification is required")), connect.CodeInvalidArgument, "classification is required"},
		{"unknown", errors.New("database password secret"), connect.CodeInternal, "An unknown error has occurred"},
		{"wrapped canceled", fmt.Errorf("private context: %w", context.Canceled), connect.CodeInternal, "An unknown error has occurred"},
		{"wrapped deadline", fmt.Errorf("private context: %w", context.DeadlineExceeded), connect.CodeInternal, "An unknown error has occurred"},
	} {
		t.Run(row.name, func(t *testing.T) {

			client := adminClient(t, &fakeAdmin{err: row.err}, "admin")
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

			for _, call := range calls {
				t.Run(call.name, func(t *testing.T) {
					err := call.call()
					var transportError *connect.Error
					if !errors.As(err, &transportError) || transportError.Code() != row.code || transportError.Message() != row.message {
						t.Fatalf("error = %v, want %v: %s", err, row.code, row.message)
					}
				})
			}
		})
	}
}
