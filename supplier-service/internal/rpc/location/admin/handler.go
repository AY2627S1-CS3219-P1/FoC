package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	location "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/admin"
	domainshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
	"google.golang.org/genproto/googleapis/type/timeofday"
)

// LocationAdmin is the domain operation set consumed by this adapter.
type LocationAdmin interface {
	Create(context.Context, location.AdminCreateRequest) (domainshared.Location, error)
	Update(context.Context, location.AdminUpdateRequest) (domainshared.Location, error)
	Archive(context.Context, string) (domainshared.Location, error)
	Unarchive(context.Context, string) (domainshared.Location, error)
}

type Server struct {
	locationv1connect.UnimplementedLocationAdminServiceHandler
	service LocationAdmin
}

func NewServer(service LocationAdmin) *Server {
	return &Server{service: service}
}

func (s *Server) CreateLocation(ctx context.Context, req *connect.Request[locationv1.CreateLocationRequest]) (*connect.Response[locationv1.CreateLocationResponse], error) {
	input := req.Msg.GetLocation()
	if input == nil || input.IsSupplier == nil || input.Coordinates == nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("classification and coordinates are required"))
	}
	if err := checkClockPrecision(input.OpensAt, input.ClosesAt); err != nil {
		return nil, err
	}
	loc, err := s.service.Create(ctx, location.AdminCreateRequest{Key: req.Msg.GetIdempotencyKey(), Input: fromProtoLocationInput(input)})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.CreateLocationResponse{Location: rpcshared.ToProtoLocation(loc)}), nil
}

func (s *Server) UpdateLocation(ctx context.Context, req *connect.Request[locationv1.UpdateLocationRequest]) (*connect.Response[locationv1.UpdateLocationResponse], error) {
	paths := req.Msg.GetUpdateMask().GetPaths()
	input := req.Msg.GetLocation()
	if input != nil {
		for _, path := range paths {
			switch path {
			case "opens_at":
				if err := checkClockPrecision(input.OpensAt); err != nil {
					return nil, err
				}
			case "closes_at":
				if err := checkClockPrecision(input.ClosesAt); err != nil {
					return nil, err
				}
			}
		}
	}
	loc, err := s.service.Update(ctx, location.AdminUpdateRequest{ID: req.Msg.GetId(), ExpectedRevision: req.Msg.GetExpectedRevision(), Paths: paths, Input: fromProtoLocationInput(input)})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.UpdateLocationResponse{Location: rpcshared.ToProtoLocation(loc)}), nil
}

func (s *Server) ArchiveLocation(ctx context.Context, req *connect.Request[locationv1.ArchiveLocationRequest]) (*connect.Response[locationv1.ArchiveLocationResponse], error) {
	loc, err := s.service.Archive(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.ArchiveLocationResponse{Location: rpcshared.ToProtoLocation(loc)}), nil
}

func (s *Server) UnarchiveLocation(ctx context.Context, req *connect.Request[locationv1.UnarchiveLocationRequest]) (*connect.Response[locationv1.UnarchiveLocationResponse], error) {
	loc, err := s.service.Unarchive(ctx, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.UnarchiveLocationResponse{Location: rpcshared.ToProtoLocation(loc)}), nil
}

func fromProtoLocationInput(in *locationv1.LocationInput) location.Input {
	if in == nil {
		return location.Input{}
	}
	out := location.Input{Name: in.Name, IsSupplier: in.GetIsSupplier(), CategoryIDs: in.CategoryIds, BuildingID: in.BuildingId,
		Floor: in.Floor, OpensAt: fromProtoClock(in.OpensAt), ClosesAt: fromProtoClock(in.ClosesAt), Contact: in.Contact, Details: in.Details}
	if in.Coordinates != nil {
		out.Coordinates = &domainshared.Coordinates{Latitude: in.Coordinates.Latitude, Longitude: in.Coordinates.Longitude}
	}
	return out
}

func fromProtoClock(in *timeofday.TimeOfDay) *domainshared.Clock {
	if in == nil {
		return nil
	}
	return &domainshared.Clock{Hour: in.Hours, Minute: in.Minutes}
}

func checkClockPrecision(times ...*timeofday.TimeOfDay) error {
	for _, value := range times {
		if value != nil && (value.Seconds != 0 || value.Nanos != 0) {
			return connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("opening hours must use whole minutes"))
		}
	}
	return nil
}
