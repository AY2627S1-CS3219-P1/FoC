package rpc

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"github.com/go-chi/chi/v5"
)

const locationID = "c0a3f4c4-12f0-4c17-aa44-8cdf6e76c94b"

type fakeLocationReader struct {
	locations map[string]location.Location
	err       error
}

func (f *fakeLocationReader) GetLocation(_ context.Context, id string) (location.Location, error) {
	if f.err != nil {
		return location.Location{}, f.err
	}
	loc, ok := f.locations[id]
	if !ok {
		return location.Location{}, location.ErrNotFound
	}
	return loc, nil
}

func (f *fakeLocationReader) ListLocations(context.Context, location.Query) ([]location.Location, int64, error) {
	if f.err != nil {
		return nil, 0, f.err
	}
	var out []location.Location
	for _, loc := range f.locations {
		out = append(out, loc)
	}
	return out, int64(len(out)), nil
}

func (f *fakeLocationReader) ListBuildings(context.Context) ([]location.Building, error) {
	return []location.Building{{ID: "b", Name: "COM2"}}, f.err
}

func (f *fakeLocationReader) ListCategories(context.Context) ([]location.Category, error) {
	return []location.Category{{ID: "c", Name: "Food"}}, f.err
}

// newLocationClient serves the Location handler behind the real auth
// middleware and returns a client calling as role, or without a token if
// role is empty.
func newLocationClient(t *testing.T, reader location.Reader, role string) locationv1connect.LocationDiscoveryServiceClient {
	t.Helper()
	auth := newTestAuth(t)
	path, handler := locationv1connect.NewLocationDiscoveryServiceHandler(
		NewLocationServer(location.NewService(reader)),
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	router := chi.NewRouter()
	router.Mount(path, auth.authenticator.Authenticate(handler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	var options []connect.ClientOption
	if role != "" {
		options = append(options, bearer(auth.token(t, role)))
	}
	return locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL, options...)
}

func TestGetLocationReturnsLocation(t *testing.T) {
	archivedAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	floor := "B1"
	client := newLocationClient(t, &fakeLocationReader{locations: map[string]location.Location{
		locationID: {
			ID:          locationID,
			Name:        "Supper Stretch",
			IsSupplier:  true,
			Building:    location.Building{ID: "b", Name: "PGP"},
			Categories:  []location.Category{{ID: "c", Name: "Food"}},
			Floor:       &floor,
			Coordinates: location.Coordinates{Latitude: 1.29, Longitude: 103.78},
			OpensAt:     &location.Clock{Hour: 22},
			ClosesAt:    &location.Clock{Hour: 2},
			ArchivedAt:  &archivedAt,
			CurrentDisablement: &location.Disablement{
				ID: "d", StartsAt: archivedAt, Reason: "Renovation",
			},
			Revision: 3,
		},
	}}, "user")

	res, err := client.GetLocation(context.Background(), connect.NewRequest(&locationv1.GetLocationRequest{Id: locationID}))
	if err != nil {
		t.Fatal(err)
	}
	got := res.Msg.Location
	if got.Name != "Supper Stretch" || got.Building.Name != "PGP" || len(got.Categories) != 1 ||
		got.GetFloor() != "B1" || got.GetOpensAt().GetHours() != 22 || got.GetClosesAt().GetHours() != 2 ||
		got.Coordinates.Latitude != 1.29 || !got.ArchivedAt.AsTime().Equal(archivedAt) ||
		got.CurrentDisablement.GetReason() != "Renovation" || got.CurrentDisablement.EndsAt != nil ||
		got.Revision != 3 {
		t.Fatalf("location = %v", got)
	}
}

func TestLocationErrors(t *testing.T) {
	cases := []struct {
		name   string
		role   string
		reader *fakeLocationReader
		call   func(locationv1connect.LocationDiscoveryServiceClient) error
		want   connect.Code
	}{
		{
			name:   "no token",
			role:   "",
			reader: &fakeLocationReader{},
			call: func(c locationv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListBuildings(context.Background(), connect.NewRequest(&locationv1.ListBuildingsRequest{}))
				return err
			},
			want: connect.CodeUnauthenticated,
		},
		{
			name:   "missing location",
			role:   "user",
			reader: &fakeLocationReader{},
			call: func(c locationv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.GetLocation(context.Background(), connect.NewRequest(&locationv1.GetLocationRequest{Id: locationID}))
				return err
			},
			want: connect.CodeNotFound,
		},
		{
			name:   "malformed id",
			role:   "user",
			reader: &fakeLocationReader{},
			call: func(c locationv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.GetLocation(context.Background(), connect.NewRequest(&locationv1.GetLocationRequest{Id: "nope"}))
				return err
			},
			want: connect.CodeInvalidArgument,
		},
		{
			name:   "page size above maximum",
			role:   "user",
			reader: &fakeLocationReader{},
			call: func(c locationv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{PageSize: 101}))
				return err
			},
			want: connect.CodeInvalidArgument,
		},
		{
			name:   "archived view without admin",
			role:   "user",
			reader: &fakeLocationReader{},
			call: func(c locationv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{
					StatusView: locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ARCHIVED,
				}))
				return err
			},
			want: connect.CodePermissionDenied,
		},
		{
			name:   "database failure",
			role:   "user",
			reader: &fakeLocationReader{err: errors.New("connection refused to 10.0.0.5")},
			call: func(c locationv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{}))
				return err
			},
			want: connect.CodeInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(newLocationClient(t, tc.reader, tc.role))
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("code = %v, want %v (err %v)", got, tc.want, err)
			}
			if tc.want == connect.CodeInternal && err.Error() != "internal: An unknown error has occurred" {
				t.Fatalf("internal error leaked details: %v", err)
			}
		})
	}
}

func TestListLocationsReturnsPagination(t *testing.T) {
	client := newLocationClient(t, &fakeLocationReader{locations: map[string]location.Location{
		locationID: {ID: locationID, Name: "NUS Co-op"},
	}}, "user")
	res, err := client.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Msg.Locations) != 1 || res.Msg.Page != 1 || res.Msg.PageSize != 20 ||
		res.Msg.TotalItems != 1 || res.Msg.TotalPages != 1 {
		t.Fatalf("response = %v", res.Msg)
	}
}

func TestListReferenceData(t *testing.T) {
	client := newLocationClient(t, &fakeLocationReader{}, "user")
	buildings, err := client.ListBuildings(context.Background(), connect.NewRequest(&locationv1.ListBuildingsRequest{}))
	if err != nil || len(buildings.Msg.Buildings) != 1 || buildings.Msg.Buildings[0].Name != "COM2" {
		t.Fatalf("buildings = %v, err %v", buildings, err)
	}
	categories, err := client.ListCategories(context.Background(), connect.NewRequest(&locationv1.ListCategoriesRequest{}))
	if err != nil || len(categories.Msg.Categories) != 1 || categories.Msg.Categories[0].Name != "Food" {
		t.Fatalf("categories = %v, err %v", categories, err)
	}
}

func TestArchivedViewRequiresAdmin(t *testing.T) {
	cases := map[string]connect.Code{
		"super_admin":    0,
		"admin":          0,
		"user":           connect.CodePermissionDenied,
		"suspended_user": connect.CodePermissionDenied,
	}
	for role, want := range cases {
		t.Run(role, func(t *testing.T) {
			client := newLocationClient(t, &fakeLocationReader{}, role)
			_, err := client.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{
				StatusView: locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ARCHIVED,
			}))
			if want == 0 && err != nil {
				t.Fatalf("err = %v, want success", err)
			}
			if want != 0 && connect.CodeOf(err) != want {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), want)
			}
		})
	}
}

func TestEveryRoleCanBrowse(t *testing.T) {
	for _, role := range []string{"super_admin", "admin", "user", "suspended_user"} {
		t.Run(role, func(t *testing.T) {
			client := newLocationClient(t, &fakeLocationReader{}, role)
			if _, err := client.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{})); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInvalidTokenIsRejected(t *testing.T) {
	auth := newTestAuth(t)
	other := newTestAuth(t)
	path, handler := locationv1connect.NewLocationDiscoveryServiceHandler(NewLocationServer(location.NewService(&fakeLocationReader{})))
	router := chi.NewRouter()
	router.Mount(path, auth.authenticator.Authenticate(handler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	// Signed by a different key than the one User Service publishes.
	client := locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL, bearer(other.token(t, "admin")))
	_, err := client.ListLocations(context.Background(), connect.NewRequest(&locationv1.ListLocationsRequest{}))
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want unauthenticated", connect.CodeOf(err))
	}
}
