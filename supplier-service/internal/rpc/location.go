package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
)

// LocationServer implements Location discovery. Mutations return
// unimplemented until #51.
type LocationServer struct {
	supplierv1connect.UnimplementedLocationServiceHandler
	service *location.Service
}

func NewLocationServer(service *location.Service) *LocationServer {
	return &LocationServer{service: service}
}

func (s *LocationServer) GetLocation(
	ctx context.Context,
	req *connect.Request[supplierv1.GetLocationRequest],
) (*connect.Response[supplierv1.GetLocationResponse], error) {
	loc, err := s.service.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	return connect.NewResponse(&supplierv1.GetLocationResponse{Location: toProtoLocation(loc)}), nil
}

func (s *LocationServer) ListLocations(
	ctx context.Context,
	req *connect.Request[supplierv1.ListLocationsRequest],
) (*connect.Response[supplierv1.ListLocationsResponse], error) {
	msg := req.Msg
	page, err := s.service.List(ctx, callerFromContext(ctx), location.ListRequest{
		Search:        msg.GetSearch(),
		BuildingID:    msg.BuildingId,
		CategoryID:    msg.CategoryId,
		SuppliersOnly: msg.GetSuppliersOnly(),
		Archive:       archiveFilters[msg.GetStatusView()],
		Sort:          sortFields[msg.GetSortField()],
		Descending:    msg.GetSortDirection() == supplierv1.SortDirection_SORT_DIRECTION_DESCENDING,
		Page:          msg.GetPage(),
		PageSize:      msg.GetPageSize(),
	})
	if err != nil {
		return nil, toConnectError(ctx, err)
	}

	locations := make([]*supplierv1.Location, len(page.Locations))
	for i, loc := range page.Locations {
		locations[i] = toProtoLocation(loc)
	}
	return connect.NewResponse(&supplierv1.ListLocationsResponse{
		Locations:  locations,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalItems: page.TotalItems,
		TotalPages: page.TotalPages,
	}), nil
}

func (s *LocationServer) ListBuildings(
	ctx context.Context,
	_ *connect.Request[supplierv1.ListBuildingsRequest],
) (*connect.Response[supplierv1.ListBuildingsResponse], error) {
	buildings, err := s.service.ListBuildings(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	out := make([]*supplierv1.Building, len(buildings))
	for i, b := range buildings {
		out[i] = toProtoBuilding(b)
	}
	return connect.NewResponse(&supplierv1.ListBuildingsResponse{Buildings: out}), nil
}

func (s *LocationServer) ListCategories(
	ctx context.Context,
	_ *connect.Request[supplierv1.ListCategoriesRequest],
) (*connect.Response[supplierv1.ListCategoriesResponse], error) {
	categories, err := s.service.ListCategories(ctx)
	if err != nil {
		return nil, toConnectError(ctx, err)
	}
	return connect.NewResponse(&supplierv1.ListCategoriesResponse{Categories: toProtoCategories(categories)}), nil
}

// callerFromContext returns a non-admin Caller until JWT verification lands,
// so admin-only views are denied rather than exposed.
func callerFromContext(_ context.Context) location.Caller {
	return location.Caller{}
}

var archiveFilters = map[supplierv1.LocationStatusView]location.ArchiveFilter{
	supplierv1.LocationStatusView_LOCATION_STATUS_VIEW_ACTIVE:   location.ArchiveActive,
	supplierv1.LocationStatusView_LOCATION_STATUS_VIEW_ARCHIVED: location.ArchiveArchived,
	supplierv1.LocationStatusView_LOCATION_STATUS_VIEW_ALL:      location.ArchiveAll,
}

var sortFields = map[supplierv1.LocationSortField]location.SortField{
	supplierv1.LocationSortField_LOCATION_SORT_FIELD_NAME:     location.SortByName,
	supplierv1.LocationSortField_LOCATION_SORT_FIELD_BUILDING: location.SortByBuilding,
}

// toConnectError returns stable public errors and logs unexpected causes.
func toConnectError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, location.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, location.ErrNotFound)
	case errors.Is(err, location.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, location.ErrPermissionDenied)
	case errors.Is(err, location.ErrInvalidArgument):
		return connect.NewError(connect.CodeInvalidArgument, location.ErrInvalidArgument)
	default:
		slog.ErrorContext(ctx, "location rpc failed", "error", err)
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
}
