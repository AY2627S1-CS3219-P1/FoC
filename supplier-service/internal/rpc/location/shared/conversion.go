package shared

import (
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	location "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ToProtoLocation(loc location.Location) *locationv1.Location {
	out := &locationv1.Location{
		Id:          loc.ID,
		Name:        loc.Name,
		IsSupplier:  loc.IsSupplier,
		Building:    ToProtoBuilding(loc.Building),
		Categories:  ToProtoCategories(loc.Categories),
		Floor:       loc.Floor,
		Coordinates: ToProtoCoordinates(loc.Coordinates),
		OpensAt:     ToProtoTimeOfDay(loc.OpensAt),
		ClosesAt:    ToProtoTimeOfDay(loc.ClosesAt),
		Contact:     loc.Contact,
		Details:     loc.Details,
		Revision:    loc.Revision,
		CreatedAt:   timestamppb.New(loc.CreatedAt),
		UpdatedAt:   timestamppb.New(loc.UpdatedAt),
	}
	if loc.ArchivedAt != nil {
		out.ArchivedAt = timestamppb.New(*loc.ArchivedAt)
	}
	if d := loc.CurrentDisablement; d != nil {
		out.CurrentDisablement = &locationv1.Disablement{
			Id:       d.ID,
			StartsAt: timestamppb.New(d.StartsAt),
			Reason:   d.Reason,
		}
		if d.EndsAt != nil {
			out.CurrentDisablement.EndsAt = timestamppb.New(*d.EndsAt)
		}
	}
	return out
}

func ToProtoBuilding(b location.Building) *locationv1.Building {
	return &locationv1.Building{
		Id:      b.ID,
		Name:    b.Name,
		Center:  ToProtoCoordinates(b.Center),
		RadiusM: b.RadiusM,
	}
}

func ToProtoCategories(categories []location.Category) []*locationv1.Category {
	out := make([]*locationv1.Category, len(categories))
	for i, c := range categories {
		out[i] = &locationv1.Category{Id: c.ID, Name: c.Name}
	}
	return out
}

func ToProtoCoordinates(c location.Coordinates) *locationv1.Coordinates {
	return &locationv1.Coordinates{Latitude: c.Latitude, Longitude: c.Longitude}
}

func ToProtoTimeOfDay(c *location.Clock) *timeofday.TimeOfDay {
	if c == nil {
		return nil
	}
	return &timeofday.TimeOfDay{Hours: c.Hour, Minutes: c.Minute}
}
