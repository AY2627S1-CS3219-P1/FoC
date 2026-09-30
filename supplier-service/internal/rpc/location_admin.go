package rpc

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"google.golang.org/genproto/googleapis/type/timeofday"
)

// LocationMutation is the domain operation set consumed by this adapter.
type LocationMutation interface {
	Create(context.Context, location.Caller, location.CreateRequest) (location.Location, error)
	Update(context.Context, location.Caller, location.UpdateRequest) (location.Location, error)
	Archive(context.Context, location.Caller, string) (location.Location, error)
	Unarchive(context.Context, location.Caller, string) (location.Location, error)
}

type LocationAdminServer struct {
	locationv1connect.UnimplementedLocationAdminServiceHandler
	service LocationMutation
}

func NewLocationAdminServer(service LocationMutation) *LocationAdminServer {
	return &LocationAdminServer{service: service}
}

// AdminAuthorizationInterceptor runs before validation, including for invalid
// requests, so an ordinary caller cannot inspect the administrator contract.
func AdminAuthorizationInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, err := requireCaller(ctx)
			if err != nil {
				return nil, err
			}
			if !caller.Admin {
				return nil, api.ToConnectError(ctx, location.ErrPermissionDenied)
			}
			return next(ctx, req)
		}
	})
}

func (s *LocationAdminServer) CreateLocation(ctx context.Context, req *connect.Request[locationv1.CreateLocationRequest]) (*connect.Response[locationv1.CreateLocationResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	input := req.Msg.GetLocation()
	if input == nil || input.IsSupplier == nil || input.Coordinates == nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("classification and coordinates are required"))
	}
	if err := checkClockPrecision(input.OpensAt, input.ClosesAt); err != nil {
		return nil, err
	}
	loc, err := s.service.Create(ctx, caller, location.CreateRequest{Key: req.Msg.GetIdempotencyKey(), Input: fromProtoLocationInput(input)})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.CreateLocationResponse{Location: toProtoLocation(loc)}), nil
}

func (s *LocationAdminServer) UpdateLocation(ctx context.Context, req *connect.Request[locationv1.UpdateLocationRequest]) (*connect.Response[locationv1.UpdateLocationResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
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
	loc, err := s.service.Update(ctx, caller, location.UpdateRequest{ID: req.Msg.GetId(), ExpectedRevision: req.Msg.GetExpectedRevision(), Paths: paths, Input: fromProtoLocationInput(input)})
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.UpdateLocationResponse{Location: toProtoLocation(loc)}), nil
}

func (s *LocationAdminServer) ArchiveLocation(ctx context.Context, req *connect.Request[locationv1.ArchiveLocationRequest]) (*connect.Response[locationv1.ArchiveLocationResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	loc, err := s.service.Archive(ctx, caller, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.ArchiveLocationResponse{Location: toProtoLocation(loc)}), nil
}

func (s *LocationAdminServer) UnarchiveLocation(ctx context.Context, req *connect.Request[locationv1.UnarchiveLocationRequest]) (*connect.Response[locationv1.UnarchiveLocationResponse], error) {
	caller, err := requireCaller(ctx)
	if err != nil {
		return nil, err
	}
	loc, err := s.service.Unarchive(ctx, caller, req.Msg.GetId())
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	return connect.NewResponse(&locationv1.UnarchiveLocationResponse{Location: toProtoLocation(loc)}), nil
}

func fromProtoLocationInput(in *locationv1.LocationInput) location.Input {
	if in == nil {
		return location.Input{}
	}
	out := location.Input{Name: in.Name, IsSupplier: in.GetIsSupplier(), CategoryIDs: in.CategoryIds, BuildingID: in.BuildingId,
		Floor: in.Floor, OpensAt: fromProtoClock(in.OpensAt), ClosesAt: fromProtoClock(in.ClosesAt), Contact: in.Contact, Details: in.Details}
	if in.Coordinates != nil {
		out.Coordinates = &location.Coordinates{Latitude: in.Coordinates.Latitude, Longitude: in.Coordinates.Longitude}
	}
	return out
}

func fromProtoClock(in *timeofday.TimeOfDay) *location.Clock {
	if in == nil {
		return nil
	}
	return &location.Clock{Hour: in.Hours, Minute: in.Minutes}
}

func checkClockPrecision(times ...*timeofday.TimeOfDay) error {
	for _, value := range times {
		if value != nil && (value.Seconds != 0 || value.Nanos != 0) {
			return connect.NewError(connect.CodeInvalidArgument, errs.NewBadRequestError("opening hours must use whole minutes"))
		}
	}
	return nil
}
