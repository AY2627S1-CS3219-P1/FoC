package rpc

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
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

func newLocationClient(t *testing.T, reader location.Reader) supplierv1connect.LocationDiscoveryServiceClient {
	t.Helper()
	path, handler := supplierv1connect.NewLocationDiscoveryServiceHandler(
		NewLocationServer(location.NewService(reader)),
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	router := chi.NewRouter()
	router.Mount(path, handler)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return supplierv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL)
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
	}})

	res, err := client.GetLocation(context.Background(), connect.NewRequest(&supplierv1.GetLocationRequest{Id: locationID}))
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
		reader *fakeLocationReader
		call   func(supplierv1connect.LocationDiscoveryServiceClient) error
		want   connect.Code
	}{
		{
			name:   "missing location",
			reader: &fakeLocationReader{},
			call: func(c supplierv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.GetLocation(context.Background(), connect.NewRequest(&supplierv1.GetLocationRequest{Id: locationID}))
				return err
			},
			want: connect.CodeNotFound,
		},
		{
			name:   "malformed id",
			reader: &fakeLocationReader{},
			call: func(c supplierv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.GetLocation(context.Background(), connect.NewRequest(&supplierv1.GetLocationRequest{Id: "nope"}))
				return err
			},
			want: connect.CodeInvalidArgument,
		},
		{
			name:   "page size above maximum",
			reader: &fakeLocationReader{},
			call: func(c supplierv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListLocations(context.Background(), connect.NewRequest(&supplierv1.ListLocationsRequest{PageSize: 101}))
				return err
			},
			want: connect.CodeInvalidArgument,
		},
		{
			name:   "archived view without admin",
			reader: &fakeLocationReader{},
			call: func(c supplierv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListLocations(context.Background(), connect.NewRequest(&supplierv1.ListLocationsRequest{
					StatusView: supplierv1.LocationStatusView_LOCATION_STATUS_VIEW_ARCHIVED,
				}))
				return err
			},
			want: connect.CodePermissionDenied,
		},
		{
			name:   "database failure",
			reader: &fakeLocationReader{err: errors.New("connection refused to 10.0.0.5")},
			call: func(c supplierv1connect.LocationDiscoveryServiceClient) error {
				_, err := c.ListLocations(context.Background(), connect.NewRequest(&supplierv1.ListLocationsRequest{}))
				return err
			},
			want: connect.CodeInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(newLocationClient(t, tc.reader))
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
	}})
	res, err := client.ListLocations(context.Background(), connect.NewRequest(&supplierv1.ListLocationsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Msg.Locations) != 1 || res.Msg.Page != 1 || res.Msg.PageSize != 20 ||
		res.Msg.TotalItems != 1 || res.Msg.TotalPages != 1 {
		t.Fatalf("response = %v", res.Msg)
	}
}

func TestListReferenceData(t *testing.T) {
	client := newLocationClient(t, &fakeLocationReader{})
	buildings, err := client.ListBuildings(context.Background(), connect.NewRequest(&supplierv1.ListBuildingsRequest{}))
	if err != nil || len(buildings.Msg.Buildings) != 1 || buildings.Msg.Buildings[0].Name != "COM2" {
		t.Fatalf("buildings = %v, err %v", buildings, err)
	}
	categories, err := client.ListCategories(context.Background(), connect.NewRequest(&supplierv1.ListCategoriesRequest{}))
	if err != nil || len(categories.Msg.Categories) != 1 || categories.Msg.Categories[0].Name != "Food" {
		t.Fatalf("categories = %v, err %v", categories, err)
	}
}

func TestLocationAdminMutationNotImplemented(t *testing.T) {
	_, handler := supplierv1connect.NewLocationAdminServiceHandler(&supplierv1connect.UnimplementedLocationAdminServiceHandler{})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := supplierv1connect.NewLocationAdminServiceClient(server.Client(), server.URL)
	_, err := client.ArchiveLocation(context.Background(), connect.NewRequest(&supplierv1.ArchiveLocationRequest{Id: locationID}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("expected unimplemented mutation: %v", err)
	}
}
