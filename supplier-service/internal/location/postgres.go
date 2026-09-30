package location

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/exterrors/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// PostgresReader reads Locations through the locationdb sqlc queries.
type PostgresReader struct {
	queries *locationdb.Queries
}

func NewPostgresReader(queries *locationdb.Queries) *PostgresReader {
	return &PostgresReader{queries: queries}
}

func (r *PostgresReader) GetLocation(ctx context.Context, id string) (Location, error) {
	locationID, err := uuid.Parse(id)
	if err != nil {
		return Location{}, ErrNotFound
	}
	row, err := r.queries.GetLocation(ctx, locationID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Location{}, ErrNotFound
	}
	if err != nil {
		return Location{}, fmt.Errorf("get location: %w", err)
	}
	// GetLocation and ListLocations select identical columns, so their row
	// types convert directly.
	rows := []locationdb.ListLocationsRow{locationdb.ListLocationsRow(row)}
	locations, err := r.toLocations(ctx, rows)
	if err != nil {
		return Location{}, err
	}
	return locations[0], nil
}

func (r *PostgresReader) ListLocations(ctx context.Context, query Query) ([]Location, int64, error) {
	buildingID, err := parseOptionalUUID(query.BuildingID)
	if err != nil {
		return nil, 0, errs.NewBadRequestError("invalid building ID")
	}
	categoryID, err := parseOptionalUUID(query.CategoryID)
	if err != nil {
		return nil, 0, errs.NewBadRequestError("invalid category ID")
	}
	filters := locationdb.CountLocationsParams{
		Search:        escapeLike(query.Search),
		RawSearch:     query.Search,
		BuildingID:    buildingID,
		CategoryID:    categoryID,
		SuppliersOnly: query.SuppliersOnly,
		ArchiveFilter: string(query.Archive),
	}

	total, err := r.queries.CountLocations(ctx, filters)
	if err != nil {
		return nil, 0, fmt.Errorf("count locations: %w", err)
	}
	rows, err := r.queries.ListLocations(ctx, locationdb.ListLocationsParams{
		Search:        filters.Search,
		RawSearch:     filters.RawSearch,
		BuildingID:    filters.BuildingID,
		CategoryID:    filters.CategoryID,
		SuppliersOnly: filters.SuppliersOnly,
		ArchiveFilter: filters.ArchiveFilter,
		SortField:     string(query.Sort),
		Descending:    query.Descending,
		PageOffset:    query.Offset,
		PageLimit:     query.Limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list locations: %w", err)
	}
	locations, err := r.toLocations(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return locations, total, nil
}

func (r *PostgresReader) ListBuildings(ctx context.Context) ([]Building, error) {
	rows, err := r.queries.ListBuildings(ctx)
	if err != nil {
		return nil, fmt.Errorf("list buildings: %w", err)
	}
	buildings := make([]Building, len(rows))
	for i, row := range rows {
		buildings[i] = Building{
			ID:      row.ID.String(),
			Name:    row.Name,
			Center:  Coordinates{Latitude: row.Latitude, Longitude: row.Longitude},
			RadiusM: row.RadiusM,
		}
	}
	return buildings, nil
}

func (r *PostgresReader) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := r.queries.ListCategories(ctx)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	categories := make([]Category, len(rows))
	for i, row := range rows {
		categories[i] = Category{ID: row.ID.String(), Name: row.Name}
	}
	return categories, nil
}

// toLocations converts rows and loads their Categories and current
// disablements in one query each.
func (r *PostgresReader) toLocations(ctx context.Context, rows []locationdb.ListLocationsRow) ([]Location, error) {
	locations := make([]Location, len(rows))
	ids := make([]uuid.UUID, len(rows))
	index := make(map[uuid.UUID]*Location, len(rows))
	for i, row := range rows {
		locations[i] = fromRow(row)
		ids[i] = row.ID
		index[row.ID] = &locations[i]
	}
	if len(rows) == 0 {
		return locations, nil
	}

	categoryRows, err := r.queries.ListLocationCategories(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list location categories: %w", err)
	}
	for _, row := range categoryRows {
		loc := index[row.LocationID]
		loc.Categories = append(loc.Categories, Category{ID: row.ID.String(), Name: row.Name})
	}

	disablementRows, err := r.queries.ListCurrentDisablements(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list current disablements: %w", err)
	}
	for _, row := range disablementRows {
		index[row.LocationID].CurrentDisablement = &Disablement{
			ID:       row.ID.String(),
			StartsAt: row.StartsAt,
			EndsAt:   row.EndsAt,
			Reason:   row.Reason,
		}
	}
	return locations, nil
}

func fromRow(row locationdb.ListLocationsRow) Location {
	return Location{
		ID:         row.ID.String(),
		Name:       row.Name,
		IsSupplier: row.IsSupplier,
		Building: Building{
			ID:      row.BuildingID.String(),
			Name:    row.BuildingName,
			Center:  Coordinates{Latitude: row.BuildingLatitude, Longitude: row.BuildingLongitude},
			RadiusM: row.BuildingRadiusM,
		},
		Floor:       row.Floor,
		Coordinates: Coordinates{Latitude: row.Latitude, Longitude: row.Longitude},
		OpensAt:     toClockPtr(row.OpenFrom),
		ClosesAt:    toClockPtr(row.OpenTo),
		Contact:     row.Contact,
		Details:     row.Details,
		ArchivedAt:  row.ArchivedAt,
		Revision:    row.Revision,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
	}
}

// escapeLike makes user input match literally inside an ILIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func parseOptionalUUID(s *string) (*uuid.UUID, error) {
	if s == nil {
		return nil, nil
	}
	id, err := uuid.Parse(*s)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// toClockPtr converts a Postgres TIME to a Clock, or nil if NULL.
func toClockPtr(t pgtype.Time) *Clock {
	if !t.Valid {
		return nil
	}
	minutes := int32(t.Microseconds / time.Minute.Microseconds())
	return &Clock{Hour: minutes / 60, Minute: minutes % 60}
}
