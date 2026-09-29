package location

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
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
	uuid, err := parseUUID(id)
	if err != nil {
		return Location{}, ErrNotFound
	}
	row, err := r.queries.GetLocation(ctx, uuid)
	if errors.Is(err, pgx.ErrNoRows) {
		return Location{}, ErrNotFound
	}
	if err != nil {
		return Location{}, fmt.Errorf("get location: %w", err)
	}
	locations := []Location{fromRow(locationdb.ListLocationsRow(row))}
	if err := r.attachDetails(ctx, locations); err != nil {
		return Location{}, err
	}
	return locations[0], nil
}

func (r *PostgresReader) ListLocations(ctx context.Context, query Query) ([]Location, int64, error) {
	buildingID, err := parseOptionalUUID(query.BuildingID)
	if err != nil {
		return nil, 0, ErrInvalidArgument
	}
	categoryID, err := parseOptionalUUID(query.CategoryID)
	if err != nil {
		return nil, 0, ErrInvalidArgument
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

	locations := make([]Location, len(rows))
	for i, row := range rows {
		locations[i] = fromRow(row)
	}
	if err := r.attachDetails(ctx, locations); err != nil {
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
			ID:      formatUUID(row.ID),
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
		categories[i] = Category{ID: formatUUID(row.ID), Name: row.Name}
	}
	return categories, nil
}

// attachDetails loads Categories and current disablements for all locations
// in two queries.
func (r *PostgresReader) attachDetails(ctx context.Context, locations []Location) error {
	if len(locations) == 0 {
		return nil
	}
	ids := make([]pgtype.UUID, len(locations))
	index := make(map[string]int, len(locations))
	for i, location := range locations {
		ids[i], _ = parseUUID(location.ID)
		index[location.ID] = i
	}

	categoryRows, err := r.queries.ListLocationCategories(ctx, ids)
	if err != nil {
		return fmt.Errorf("list location categories: %w", err)
	}
	for _, row := range categoryRows {
		i := index[formatUUID(row.LocationID)]
		locations[i].Categories = append(locations[i].Categories, Category{ID: formatUUID(row.ID), Name: row.Name})
	}

	disablementRows, err := r.queries.ListCurrentDisablements(ctx, ids)
	if err != nil {
		return fmt.Errorf("list current disablements: %w", err)
	}
	for _, row := range disablementRows {
		i := index[formatUUID(row.LocationID)]
		locations[i].CurrentDisablement = &Disablement{
			ID:       formatUUID(row.ID),
			StartsAt: row.StartsAt.Time,
			EndsAt:   timePtr(row.EndsAt),
			Reason:   row.Reason,
		}
	}
	return nil
}

func fromRow(row locationdb.ListLocationsRow) Location {
	return Location{
		ID:         formatUUID(row.ID),
		Name:       row.Name,
		IsSupplier: row.IsSupplier,
		Building: Building{
			ID:      formatUUID(row.BuildingID),
			Name:    row.BuildingName,
			Center:  Coordinates{Latitude: row.BuildingLatitude, Longitude: row.BuildingLongitude},
			RadiusM: row.BuildingRadiusM,
		},
		Floor:       textPtr(row.Floor),
		Coordinates: Coordinates{Latitude: row.Latitude, Longitude: row.Longitude},
		OpensAt:     clockPtr(row.OpenFrom),
		ClosesAt:    clockPtr(row.OpenTo),
		Contact:     textPtr(row.Contact),
		Details:     row.Details,
		ArchivedAt:  timePtr(row.ArchivedAt),
		Revision:    row.Revision,
		CreatedAt:   row.CreatedAt.Time,
		UpdatedAt:   row.UpdatedAt.Time,
	}
}

// escapeLike makes user input match literally inside an ILIKE pattern.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func parseUUID(s string) (pgtype.UUID, error) {
	var id pgtype.UUID
	err := id.Scan(s)
	return id, err
}

func parseOptionalUUID(s *string) (pgtype.UUID, error) {
	if s == nil {
		return pgtype.UUID{}, nil
	}
	return parseUUID(*s)
}

func formatUUID(id pgtype.UUID) string {
	b := id.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}

func clockPtr(t pgtype.Time) *string {
	if !t.Valid {
		return nil
	}
	minutes := t.Microseconds / 60_000_000
	s := fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
	return &s
}
