package seed

import (
	"context"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/seeddb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

var seedNamespace = uuid.NewSHA1(
	uuid.NameSpaceURL,
	[]byte("https://github.com/AY2627S1-CS3219-P1/FoC/supplier-service/seed/v1"),
)

// Seed validates all inputs, then applies non-empty seed fields in one transaction.
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

	report, err := importDataset(ctx, seeddb.New(tx), data)
	if err != nil {
		return Report{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("commit seed transaction: %w", err)
	}
	return report, nil
}

func importDataset(ctx context.Context, queries *seeddb.Queries, data dataset) (Report, error) {
	var report Report
	buildingIDs := make(map[string]pgtype.UUID, len(data.buildings))
	for _, building := range data.buildings {
		candidateID := deterministicUUID(building.sourceKey)
		id, err := queries.GetBuilding(ctx, seeddb.GetBuildingParams{
			ID:   candidateID,
			Name: building.name,
		})
		id, exists, err := resolveSeedID(candidateID, id, err)
		if err != nil {
			return Report{}, fmt.Errorf("find Building %q: %w", building.sourceKey, err)
		}

		changed, err := queries.UpsertBuilding(ctx, seeddb.UpsertBuildingParams{
			ID:        id,
			Name:      building.name,
			Longitude: building.longitude,
			Latitude:  building.latitude,
			RadiusM:   building.radiusM,
		})
		if err != nil {
			return Report{}, fmt.Errorf("upsert Building %q: %w", building.sourceKey, err)
		}
		recordSeedChange(&report.Buildings, exists, changed)
		buildingIDs[building.sourceKey] = id
	}

	categoryIDs := make(map[string]pgtype.UUID, len(data.categories))
	for _, category := range data.categories {
		candidateID := deterministicUUID(category.sourceKey)
		id, err := queries.GetCategory(ctx, seeddb.GetCategoryParams{
			ID:   candidateID,
			Name: category.name,
		})
		id, exists, err := resolveSeedID(candidateID, id, err)
		if err != nil {
			return Report{}, fmt.Errorf("find Category %q: %w", category.sourceKey, err)
		}

		changed, err := queries.UpsertCategory(ctx, seeddb.UpsertCategoryParams{
			ID:   id,
			Name: category.name,
		})
		if err != nil {
			return Report{}, fmt.Errorf("upsert Category %q: %w", category.sourceKey, err)
		}
		recordSeedChange(&report.Categories, exists, changed)
		categoryIDs[category.sourceKey] = id
	}

	for _, location := range data.locations {
		locationID := deterministicUUID(location.sourceKey)
		exists, err := queries.LocationExists(ctx, locationID)
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
		if !exists && location.isSupplier {
			if location.openFrom == nil {
				return Report{}, fmt.Errorf("new Supplier Location %q requires opening hours", location.sourceKey)
			}
			if !location.categoriesProvided {
				return Report{}, fmt.Errorf("new Supplier Location %q requires a non-empty Type", location.sourceKey)
			}
		}
		changed, err := queries.UpsertLocation(ctx, seeddb.UpsertLocationParams{
			ID:         locationID,
			Name:       location.name,
			IsSupplier: location.isSupplier,
			BuildingID: buildingID,
			Floor:      database.ToPGNullableText(location.floor),
			Longitude:  location.longitude,
			Latitude:   location.latitude,
			OpenFrom:   database.ToPGTimeOfDay(location.openFrom),
			OpenTo:     database.ToPGTimeOfDay(location.openTo),
			Contact:    database.ToPGNullableText(location.contact),
			Details:    database.ToPGNullableText(location.details),
		})
		if err != nil {
			return Report{}, fmt.Errorf("upsert Location %q: %w", location.sourceKey, err)
		}
		recordSeedChange(counts, exists, changed)

		if !location.categoriesProvided {
			continue
		}
		if !location.isSupplier {
			removed, err := queries.DeleteAllLocationCategories(ctx, locationID)
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
	queries *seeddb.Queries,
	location location,
	locationID pgtype.UUID,
	categoryIDs map[string]pgtype.UUID,
	report *Report,
) error {
	existingIDs, err := queries.ListLocationCategoryIDs(ctx, locationID)
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
		if err := queries.AddLocationCategory(ctx, seeddb.AddLocationCategoryParams{
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
		if err := queries.DeleteLocationCategory(ctx, seeddb.DeleteLocationCategoryParams{
			LocationID: locationID,
			CategoryID: categoryID,
		}); err != nil {
			return fmt.Errorf("remove stale Category from Location %q: %w", location.sourceKey, err)
		}
		report.LocationCategories.Updated++
	}
	return nil
}
