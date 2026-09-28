package seed

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/sqlc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var seedNamespace = uuid.NewSHA1(
	uuid.NameSpaceURL,
	[]byte("https://github.com/AY2627S1-CS3219-P1/FoC/supplier-service/seed/v1"),
)

// Seed validates all inputs, then upserts the complete dataset in one transaction.
func Seed(ctx context.Context, pool *pgxpool.Pool, paths Paths) (Report, error) {
	data, err := load(paths)
	if err != nil {
		return Report{}, fmt.Errorf("validate seed data: %w", err)
	}

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return Report{}, fmt.Errorf("begin seed transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	report, err := importDataset(ctx, sqlc.New(tx), data)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit seed transaction: %w", err)
	}
	return report, nil
}

func importDataset(ctx context.Context, queries *sqlc.Queries, data dataset) (Report, error) {
	var report Report
	buildingIDs := make(map[string]pgtype.UUID, len(data.buildings))
	for _, building := range data.buildings {
		candidateID := deterministicUUID(building.sourceKey)
		id, err := queries.GetSeedBuilding(ctx, sqlc.GetSeedBuildingParams{
			ID:   candidateID,
			Name: building.name,
		})
		existed := true
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			id = candidateID
			existed = false
		case err != nil:
			return Report{}, fmt.Errorf("find Building %q: %w", building.sourceKey, err)
		}

		changed, err := queries.UpsertSeedBuilding(ctx, sqlc.UpsertSeedBuildingParams{
			ID:        id,
			Name:      building.name,
			Longitude: building.longitude,
			Latitude:  building.latitude,
			RadiusM:   building.radiusM,
		})
		if err != nil {
			return Report{}, fmt.Errorf("upsert Building %q: %w", building.sourceKey, err)
		}
		if !existed {
			report.Buildings.Inserted++
		} else if changed > 0 {
			report.Buildings.Updated++
		}
		buildingIDs[building.sourceKey] = id
	}

	categoryIDs := make(map[string]pgtype.UUID, len(data.categories))
	for _, category := range data.categories {
		candidateID := deterministicUUID(category.sourceKey)
		id, err := queries.GetSeedCategory(ctx, sqlc.GetSeedCategoryParams{
			ID:   candidateID,
			Name: category.name,
		})
		existed := true
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			id = candidateID
			existed = false
		case err != nil:
			return Report{}, fmt.Errorf("find Category %q: %w", category.sourceKey, err)
		}

		changed, err := queries.UpsertSeedCategory(ctx, sqlc.UpsertSeedCategoryParams{
			ID:   id,
			Name: category.name,
		})
		if err != nil {
			return Report{}, fmt.Errorf("upsert Category %q: %w", category.sourceKey, err)
		}
		if !existed {
			report.Categories.Inserted++
		} else if changed > 0 {
			report.Categories.Updated++
		}
		categoryIDs[category.sourceKey] = id
	}

	for _, location := range data.locations {
		locationID := deterministicUUID(location.sourceKey)
		exists, err := queries.SeedLocationExists(ctx, locationID)
		if err != nil {
			return Report{}, fmt.Errorf("find Location %q: %w", location.sourceKey, err)
		}
		counts := &report.OrdinaryLocations
		if location.isSupplier {
			counts = &report.SupplierLocations
		}

		buildingID, buildingExists := buildingIDs[location.buildingKey]
		if !buildingExists {
			return Report{}, fmt.Errorf("Location %q references unknown Building %q", location.sourceKey, location.buildingKey)
		}
		changed, err := queries.UpsertSeedLocation(ctx, sqlc.UpsertSeedLocationParams{
			ID:         locationID,
			Name:       location.name,
			IsSupplier: location.isSupplier,
			BuildingID: buildingID,
			Floor:      nullableText(location.floor),
			Longitude:  location.longitude,
			Latitude:   location.latitude,
			OpenFrom:   nullableTime(location.openFrom),
			OpenTo:     nullableTime(location.openTo),
			Contact:    nullableText(location.contact),
			Details:    location.details,
		})
		if err != nil {
			return Report{}, fmt.Errorf("upsert Location %q: %w", location.sourceKey, err)
		}
		if !exists {
			counts.Inserted++
		} else if changed > 0 {
			counts.Updated++
		}

		if !location.isSupplier {
			removed, err := queries.DeleteAllSeedLocationCategories(ctx, locationID)
			if err != nil {
				return Report{}, fmt.Errorf("clear Categories for ordinary Location %q: %w", location.sourceKey, err)
			}
			report.LocationCategories.Updated += int(removed)
			continue
		}
		if err := syncLocationCategories(ctx, queries, location, locationID, categoryIDs, &report); err != nil {
			return Report{}, err
		}
	}

	return report, nil
}

func syncLocationCategories(
	ctx context.Context,
	queries *sqlc.Queries,
	location location,
	locationID pgtype.UUID,
	categoryIDs map[string]pgtype.UUID,
	report *Report,
) error {
	existingIDs, err := queries.ListSeedLocationCategoryIDs(ctx, locationID)
	if err != nil {
		return fmt.Errorf("list Categories for Location %q: %w", location.sourceKey, err)
	}
	existing := make(map[pgtype.UUID]struct{}, len(existingIDs))
	for _, id := range existingIDs {
		existing[id] = struct{}{}
	}

	desired := make(map[pgtype.UUID]struct{}, len(location.categories))
	for _, categoryKey := range location.categories {
		categoryID, exists := categoryIDs[categoryKey]
		if !exists {
			return fmt.Errorf("Location %q references unknown Category %q", location.sourceKey, categoryKey)
		}
		desired[categoryID] = struct{}{}
		if _, exists := existing[categoryID]; exists {
			continue
		}
		if err := queries.AddSeedLocationCategory(ctx, sqlc.AddSeedLocationCategoryParams{
			LocationID: locationID,
			CategoryID: categoryID,
		}); err != nil {
			return fmt.Errorf("add Category %q to Location %q: %w", categoryKey, location.sourceKey, err)
		}
		report.LocationCategories.Inserted++
	}

	for categoryID := range existing {
		if _, keep := desired[categoryID]; keep {
			continue
		}
		if err := queries.DeleteSeedLocationCategory(ctx, sqlc.DeleteSeedLocationCategoryParams{
			LocationID: locationID,
			CategoryID: categoryID,
		}); err != nil {
			return fmt.Errorf("remove stale Category from Location %q: %w", location.sourceKey, err)
		}
		report.LocationCategories.Updated++
	}
	return nil
}

func deterministicUUID(sourceKey string) pgtype.UUID {
	id := uuid.NewSHA1(seedNamespace, []byte(sourceKey))
	return pgtype.UUID{Bytes: id, Valid: true}
}

func nullableText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func nullableTime(value *time.Time) pgtype.Time {
	if value == nil {
		return pgtype.Time{}
	}
	microseconds := int64(value.Hour()*60*60+value.Minute()*60+value.Second()) * 1_000_000
	return pgtype.Time{Microseconds: microseconds, Valid: true}
}
