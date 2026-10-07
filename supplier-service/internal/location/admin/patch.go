package admin

import (
	shared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"slices"
)

var writablePaths = map[string]bool{
	"name": true, "is_supplier": true, "category_ids": true, "building_id": true,
	"floor": true, "coordinates": true, "opens_at": true, "closes_at": true,
	"contact": true, "details": true,
}

func validateMask(paths []string) (map[string]bool, error) {
	if len(paths) == 0 {
		return nil, ErrInvalidArgument
	}
	mask := make(map[string]bool, len(paths))
	for _, path := range paths {
		if !writablePaths[path] || mask[path] {
			return nil, ErrInvalidArgument
		}
		mask[path] = true
	}
	if mask["opens_at"] != mask["closes_at"] {
		return nil, ErrInvalidArgument
	}
	return mask, nil
}

func mergeInput(current shared.Location, patch Input, mask map[string]bool) Input {
	categoryIDs := make([]string, len(current.Categories))
	for i, category := range current.Categories {
		categoryIDs[i] = category.ID
	}
	coordinates := current.Coordinates
	in := Input{
		Name: current.Name, IsSupplier: current.IsSupplier, CategoryIDs: categoryIDs,
		BuildingID: current.Building.ID, Floor: current.Floor, Coordinates: &coordinates,
		OpensAt: current.OpensAt, ClosesAt: current.ClosesAt, Contact: current.Contact,
		Details: current.Details,
	}
	if mask["name"] {
		in.Name = patch.Name
	}
	if mask["is_supplier"] {
		in.IsSupplier = patch.IsSupplier
	}
	if mask["category_ids"] {
		in.CategoryIDs = slices.Clone(patch.CategoryIDs)
	}
	if mask["building_id"] {
		in.BuildingID = patch.BuildingID
	}
	if mask["floor"] {
		in.Floor = patch.Floor
	}
	if mask["coordinates"] {
		in.Coordinates = patch.Coordinates
	}
	if mask["opens_at"] {
		in.OpensAt, in.ClosesAt = patch.OpensAt, patch.ClosesAt
	}
	if mask["contact"] {
		in.Contact = patch.Contact
	}
	if mask["details"] {
		in.Details = patch.Details
	}
	return in
}
