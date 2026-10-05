// Package discovery implements Location discovery independently of transport
// and persistence.
package discovery

import (
	"context"
)

type Service struct {
	reader Reader
}

func NewService(reader Reader) *Service {
	return &Service{reader: reader}
}

// Get returns active and archived Locations so old references still resolve.
func (s *Service) Get(ctx context.Context, id string) (Location, error) {
	return s.reader.GetLocation(ctx, id)
}

func (s *Service) List(ctx context.Context, caller Caller, req ListRequest) (Page, error) {
	query, page, pageSize, err := normalizeList(caller, req)
	if err != nil {
		return Page{}, err
	}
	locations, total, err := s.reader.ListLocations(ctx, query)
	if err != nil {
		return Page{}, err
	}
	return Page{
		Locations:  locations,
		Page:       page,
		PageSize:   pageSize,
		TotalItems: total,
		TotalPages: int32((total + int64(pageSize) - 1) / int64(pageSize)),
	}, nil
}

func (s *Service) ListBuildings(ctx context.Context) ([]Building, error) {
	return s.reader.ListBuildings(ctx)
}

func (s *Service) ListCategories(ctx context.Context) ([]Category, error) {
	return s.reader.ListCategories(ctx)
}
