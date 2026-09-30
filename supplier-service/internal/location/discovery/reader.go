package discovery

import "context"

// Reader returns ErrNotFound for missing Locations.
type Reader interface {
	GetLocation(ctx context.Context, id string) (Location, error)
	ListLocations(ctx context.Context, query Query) ([]Location, int64, error)
	ListBuildings(ctx context.Context) ([]Building, error)
	ListCategories(ctx context.Context) ([]Category, error)
}
