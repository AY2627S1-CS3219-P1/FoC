package discovery

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
)

type Location = shared.Location
type Clock = shared.Clock
type Coordinates = shared.Coordinates
type Building = shared.Building
type Category = shared.Category
type Disablement = shared.Disablement
type Caller = auth.Caller

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	MaxSearchLength = 200
)

var (
	ErrNotFound         = shared.ErrNotFound
	ErrPermissionDenied = shared.ErrPermissionDenied
)

type ArchiveFilter string

const (
	ArchiveActive   ArchiveFilter = "active"
	ArchiveArchived ArchiveFilter = "archived"
	ArchiveAll      ArchiveFilter = "all"
)

type SortField string

const (
	SortByName     SortField = "name"
	SortByBuilding SortField = "building"
)

// ListRequest is a caller's list request. Zero values select defaults.
type ListRequest struct {
	Search        string
	BuildingID    *string
	CategoryID    *string
	SuppliersOnly bool
	Archive       ArchiveFilter
	Sort          SortField
	Descending    bool
	Page          int32
	PageSize      int32
}

// Query is a normalized ListRequest for a Reader.
type Query struct {
	Search        string
	BuildingID    *string
	CategoryID    *string
	SuppliersOnly bool
	Archive       ArchiveFilter
	Sort          SortField
	Descending    bool
	Offset        int32
	Limit         int32
}

type Page struct {
	Locations  []Location
	Page       int32
	PageSize   int32
	TotalItems int64
	TotalPages int32
}
