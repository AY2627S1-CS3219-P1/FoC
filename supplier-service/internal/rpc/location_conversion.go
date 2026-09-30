package rpc

import (
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func toProtoLocation(loc location.Location) *supplierv1.Location {
	out := &supplierv1.Location{
		Id:          loc.ID,
		Name:        loc.Name,
		IsSupplier:  loc.IsSupplier,
		Building:    toProtoBuilding(loc.Building),
		Categories:  toProtoCategories(loc.Categories),
		Floor:       loc.Floor,
		Coordinates: toProtoCoordinates(loc.Coordinates),
		OpensAt:     toProtoTimeOfDay(loc.OpensAt),
		ClosesAt:    toProtoTimeOfDay(loc.ClosesAt),
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
		out.CurrentDisablement = &supplierv1.Disablement{
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

func toProtoBuilding(b location.Building) *supplierv1.Building {
	return &supplierv1.Building{
		Id:      b.ID,
		Name:    b.Name,
		Center:  toProtoCoordinates(b.Center),
		RadiusM: b.RadiusM,
	}
}

func toProtoCategories(categories []location.Category) []*supplierv1.Category {
	out := make([]*supplierv1.Category, len(categories))
	for i, c := range categories {
		out[i] = &supplierv1.Category{Id: c.ID, Name: c.Name}
	}
	return out
}

func toProtoCoordinates(c location.Coordinates) *supplierv1.Coordinates {
	return &supplierv1.Coordinates{Latitude: c.Latitude, Longitude: c.Longitude}
}

func toProtoTimeOfDay(c *location.Clock) *timeofday.TimeOfDay {
	if c == nil {
		return nil
	}
	return &timeofday.TimeOfDay{Hours: c.Hour, Minutes: c.Minute}
}
