package discovery

import (
	"context"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	location "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
)

// LocationServer implements Location discovery.
type LocationServer struct {
	locationv1connect.UnimplementedLocationDiscoveryServiceHandler
	service *location.Service
}

func NewServer(service *location.Service) *LocationServer {
	return &LocationServer{service: service}
}

func (s *LocationServer) GetLocation(
	ctx context.Context,
	req *connect.Request[locationv1.GetLocationRequest],
) (*connect.Response[locationv1.GetLocationResponse], error) {
	loc, err := s.service.Get(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.GetLocationResponse{Location: rpcshared.ToProtoLocation(loc)}), nil
}

func (s *LocationServer) ListLocations(
	ctx context.Context,
	req *connect.Request[locationv1.ListLocationsRequest],
) (*connect.Response[locationv1.ListLocationsResponse], error) {
	caller, _ := auth.CallerFromContext(ctx)
	msg := req.Msg
	page, err := s.service.List(ctx, caller, location.ListRequest{
		Search:        msg.GetSearch(),
		BuildingID:    msg.BuildingId,
		CategoryID:    msg.CategoryId,
		SuppliersOnly: msg.GetSuppliersOnly(),
		Archive:       archiveFilters[msg.GetStatusView()],
		Sort:          sortFields[msg.GetSortField()],
		Descending:    msg.GetSortDirection() == locationv1.SortDirection_SORT_DIRECTION_DESCENDING,
		Page:          msg.GetPage(),
		PageSize:      msg.GetPageSize(),
	})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}

	locations := make([]*locationv1.Location, len(page.Locations))
	for i, loc := range page.Locations {
		locations[i] = rpcshared.ToProtoLocation(loc)
	}
	return connect.NewResponse(&locationv1.ListLocationsResponse{
		Locations:  locations,
		Page:       page.Page,
		PageSize:   page.PageSize,
		TotalItems: page.TotalItems,
		TotalPages: page.TotalPages,
	}), nil
}

func (s *LocationServer) ListBuildings(
	ctx context.Context,
	_ *connect.Request[locationv1.ListBuildingsRequest],
) (*connect.Response[locationv1.ListBuildingsResponse], error) {
	buildings, err := s.service.ListBuildings(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	out := make([]*locationv1.Building, len(buildings))
	for i, b := range buildings {
		out[i] = rpcshared.ToProtoBuilding(b)
	}
	return connect.NewResponse(&locationv1.ListBuildingsResponse{Buildings: out}), nil
}

func (s *LocationServer) ListCategories(
	ctx context.Context,
	_ *connect.Request[locationv1.ListCategoriesRequest],
) (*connect.Response[locationv1.ListCategoriesResponse], error) {
	categories, err := s.service.ListCategories(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.ListCategoriesResponse{Categories: rpcshared.ToProtoCategories(categories)}), nil
}

var archiveFilters = map[locationv1.LocationStatusView]location.ArchiveFilter{
	locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ACTIVE:   location.ArchiveActive,
	locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ARCHIVED: location.ArchiveArchived,
	locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ALL:      location.ArchiveAll,
}

var sortFields = map[locationv1.LocationSortField]location.SortField{
	locationv1.LocationSortField_LOCATION_SORT_FIELD_NAME:     location.SortByName,
	locationv1.LocationSortField_LOCATION_SORT_FIELD_BUILDING: location.SortByBuilding,
}
