// Package location implements Location discovery independently of transport
// and persistence.
package location

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/exterrors/errs"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
	MaxSearchLength = 200
)

var (
	ErrNotFound         = errs.NewNotFoundError("location not found")
	ErrPermissionDenied = errs.NewForbiddenError("permission denied")
)

type Location struct {
	ID                 string
	Name               string
	IsSupplier         bool
	Building           Building
	Categories         []Category
	Floor              *string
	Coordinates        Coordinates
	OpensAt            *Clock
	ClosesAt           *Clock
	Contact            *string
	Details            string
	ArchivedAt         *time.Time
	CurrentDisablement *Disablement
	Revision           int64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Clock is an Asia/Singapore wall-clock time of day.
type Clock struct {
	Hour   int32
	Minute int32
}

type Coordinates struct {
	Latitude  float64
	Longitude float64
}

type Building struct {
	ID      string
	Name    string
	Center  Coordinates
	RadiusM float32
}

type Category struct {
	ID   string
	Name string
}

type Disablement struct {
	ID       string
	StartsAt time.Time
	EndsAt   *time.Time
	Reason   string
}

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

// Caller is the authenticated identity making a request.
type Caller struct {
	Admin bool
}

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

// Reader returns ErrNotFound for missing Locations.
type Reader interface {
	GetLocation(ctx context.Context, id string) (Location, error)
	ListLocations(ctx context.Context, query Query) ([]Location, int64, error)
	ListBuildings(ctx context.Context) ([]Building, error)
	ListCategories(ctx context.Context) ([]Category, error)
}

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
	search := strings.TrimSpace(req.Search)
	if length := utf8.RuneCountInString(search); length > MaxSearchLength {
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("search has %d characters; maximum is %d", length, MaxSearchLength))
	}
	if req.Page < 0 {
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("page is %d; it cannot be negative", req.Page))
	}
	if req.PageSize < 0 {
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("page_size is %d; it cannot be negative", req.PageSize))
	}
	if req.PageSize > MaxPageSize {
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("page_size is %d; maximum is %d", req.PageSize, MaxPageSize))
	}

	archive := req.Archive
	switch archive {
	case "":
		archive = ArchiveActive
	case ArchiveActive:
	case ArchiveArchived, ArchiveAll:
		if !caller.Admin {
			return Page{}, ErrPermissionDenied
		}
	default:
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("archive value %q is invalid; use active, archived, or all", archive))
	}

	sort := req.Sort
	switch sort {
	case "":
		sort = SortByName
	case SortByName, SortByBuilding:
	default:
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("sort value %q is invalid; use name or building", sort))
	}

	page := max(req.Page, 1)
	pageSize := req.PageSize
	if pageSize == 0 {
		pageSize = DefaultPageSize
	}
	if int64(page-1)*int64(pageSize) > math.MaxInt32 {
		return Page{}, errs.NewBadRequestError(
			fmt.Sprintf("page %d with page_size %d exceeds the supported offset range", page, pageSize))
	}

	locations, total, err := s.reader.ListLocations(ctx, Query{
		Search:        search,
		BuildingID:    req.BuildingID,
		CategoryID:    req.CategoryID,
		SuppliersOnly: req.SuppliersOnly,
		Archive:       archive,
		Sort:          sort,
		Descending:    req.Descending,
		Offset:        (page - 1) * pageSize,
		Limit:         pageSize,
	})
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
