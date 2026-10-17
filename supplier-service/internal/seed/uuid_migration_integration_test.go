//go:build integration

package seed

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

func TestUUIDDefaultsMigration(t *testing.T) {
	databaseURL := startPostGIS(t)
	db := openSQLDB(t, databaseURL)
	migrateAll(t, db)
	pool := openPool(t, databaseURL)
	directory := migrationDirectory(t)

	// Fresh installations generate v7 IDs for every UUID primary-key table.
	fresh := insertUUIDResources(t, pool, "fresh", 7)

	if err := goose.DownTo(db, directory, 10); err != nil {
		t.Fatalf("restore v4 defaults: %v", err)
	}
	legacy := insertUUIDResources(t, pool, "legacy", 4)

	// Upgrade changes defaults while preserving rows and their relationships.
	if err := goose.Up(db, directory); err != nil {
		t.Fatalf("upgrade defaults: %v", err)
	}
	assertUUIDResourcesPresent(t, pool, fresh)
	assertUUIDResourcesPresent(t, pool, legacy)
	upgraded := insertUUIDResources(t, pool, "upgraded", 7)

	if err := goose.DownTo(db, directory, 10); err != nil {
		t.Fatalf("roll back defaults: %v", err)
	}
	assertUUIDResourcesPresent(t, pool, fresh)
	assertUUIDResourcesPresent(t, pool, legacy)
	assertUUIDResourcesPresent(t, pool, upgraded)
	rolledBack := insertUUIDResources(t, pool, "rolled-back", 4)

	if err := goose.Up(db, directory); err != nil {
		t.Fatalf("replay defaults: %v", err)
	}
	assertUUIDResourcesPresent(t, pool, rolledBack)
	insertUUIDResources(t, pool, "replayed", 7)
}

func insertUUIDResources(t *testing.T, pool *pgxpool.Pool, label string, wantVersion uuid.Version) map[string]uuid.UUID {
	t.Helper()
	ctx := context.Background()
	ids := make(map[string]uuid.UUID)
	insert := func(table, query string, args ...any) {
		t.Helper()
		var id uuid.UUID
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", table, err)
		}
		if id.Version() != wantVersion {
			t.Errorf("%s ID %s has version %d, want %d", table, id, id.Version(), wantVersion)
		}
		ids[table] = id
	}

	insert("categories", `INSERT INTO categories (name) VALUES ($1) RETURNING id`, label)
	insert("buildings", `INSERT INTO buildings (name, center, radius_m)
		VALUES ($1, ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography, 75)
		RETURNING id`, label)
	insert("locations", `INSERT INTO locations (name, building_id, coordinates)
		VALUES ($1, $2, ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography)
		RETURNING id`, label, ids["buildings"])
	insert("location_photos", `INSERT INTO location_photos (location_id, photo_url)
		VALUES ($1, 'https://example.com/photo.jpg') RETURNING id`, ids["locations"])
	insert("location_disablements", `INSERT INTO location_disablements (location_id, reason, created_by)
		VALUES ($1, 'UUID migration test', 'admin') RETURNING id`, ids["locations"])
	insert("location_addition_requests", `INSERT INTO location_addition_requests
		(name, submitted_by, building_id, coordinates)
		VALUES ($1, 'owner', $2, ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography)
		RETURNING id`, label, ids["buildings"])

	execSQL(t, pool, `INSERT INTO location_categories (location_id, category_id) VALUES ($1, $2)`, ids["locations"], ids["categories"])
	execSQL(t, pool, `INSERT INTO location_addition_request_categories (request_id, category_id) VALUES ($1, $2)`, ids["location_addition_requests"], ids["categories"])

	return ids
}

func assertUUIDResourcesPresent(t *testing.T, pool *pgxpool.Pool, ids map[string]uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	for table, id := range ids {
		var exists bool
		query := fmt.Sprintf("SELECT EXISTS (SELECT 1 FROM %s WHERE id = $1)", table)
		if err := pool.QueryRow(ctx, query, id).Scan(&exists); err != nil || !exists {
			t.Fatalf("%s lost existing ID %s: exists=%v error=%v", table, id, exists, err)
		}
	}

	var relationships int
	err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM location_categories WHERE location_id = $1 AND category_id = $2) +
		(SELECT count(*) FROM location_addition_request_categories WHERE request_id = $3 AND category_id = $2)`,
		ids["locations"], ids["categories"], ids["location_addition_requests"]).Scan(&relationships)
	if err != nil || relationships != 2 {
		t.Fatalf("category relationships changed: count=%d error=%v", relationships, err)
	}
}
