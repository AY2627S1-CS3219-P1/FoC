//go:build integration

package admin

import (
	"context"
	"database/sql"
	"errors"
	shared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/locationfixture"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

func newTestAdminService(pool *pgxpool.Pool) *Service {
	return NewService(NewPostgresStore(pool), time.Now)
}

func adminInput() Input {
	return Input{
		Name: "New supplier", IsSupplier: true, CategoryIDs: []string{foodID},
		BuildingID: com2ID, Coordinates: &shared.Coordinates{Latitude: 1.294, Longitude: 103.774},
		Details: "Pickup at counter",
	}
}

func TestPostgresAdminReferenceFailuresAndRollback(t *testing.T) {
	pool := locationfixture.SetupDatabase(t)
	ctx := context.Background()
	svc := newTestAdminService(pool)
	admin := auth.Caller{ID: "admin", Admin: true}
	for _, tc := range []struct {
		name string
		edit func(*Input)
		want error
	}{
		{"malformed building", func(in *Input) { in.BuildingID = "bad" }, ErrInvalidArgument},
		{"missing building", func(in *Input) { in.BuildingID = uuid.NewString() }, ErrFailedPrecondition},
		{"malformed category", func(in *Input) { in.CategoryIDs = []string{"bad"} }, ErrInvalidArgument},
		{"missing category", func(in *Input) { in.CategoryIDs = []string{uuid.NewString()} }, ErrFailedPrecondition},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := adminInput()
			tc.edit(&input)
			if _, err := svc.Create(auth.WithCaller(ctx, admin), CreateRequest{Key: uuid.NewString(), Input: input}); !errors.Is(err, tc.want) {
				t.Fatalf("create error = %v, want %v", err, tc.want)
			}
		})
	}
	// A valid scalar update followed by a shared.Category FK failure must leave the
	// shared.Location row and its old links unchanged.
	store := NewPostgresStore(pool)
	err := store.Within(ctx, func(tx Tx) error {
		current, err := tx.GetForUpdate(ctx, coopID)
		if err != nil {
			return err
		}
		input := adminInput()
		input.Name = "Must roll back"
		input.CategoryIDs = []string{uuid.NewString()}
		_, err = tx.Update(ctx, coopID, input, current.Revision, time.Now())
		return err
	})
	if !errors.Is(err, ErrFailedPrecondition) {
		t.Fatalf("failed Category insert = %v", err)
	}
	var name string
	var revision int64
	if err := pool.QueryRow(ctx, "SELECT name, revision FROM locations WHERE id = $1", coopID).Scan(&name, &revision); err != nil {
		t.Fatal(err)
	}
	if name != "NUS Co-op" || revision != 1 {
		t.Fatalf("rollback left name %q, revision %d", name, revision)
	}
	var category string
	if err := pool.QueryRow(ctx, "SELECT category_id::text FROM location_categories WHERE location_id = $1", coopID).Scan(&category); err != nil || category != foodID {
		t.Fatalf("rollback left category %q, %v", category, err)
	}
}

func TestPostgresAdminLockedRelationshipIsFresh(t *testing.T) {
	pool := locationfixture.SetupDatabase(t)
	ctx := context.Background()
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx) //nolint:errcheck
	if _, err := first.Exec(ctx, "SELECT id FROM locations WHERE id = $1 FOR UPDATE", coopID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec(ctx, "DELETE FROM location_categories WHERE location_id = $1", coopID); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Exec(ctx, "INSERT INTO location_categories (location_id, category_id) VALUES ($1, $2)", coopID, coffeeID); err != nil {
		t.Fatal(err)
	}
	result := make(chan shared.Location, 1)
	failure := make(chan error, 1)
	go func() {
		err := NewPostgresStore(pool).Within(ctx, func(tx Tx) error {
			location, err := tx.GetForUpdate(ctx, coopID)
			if err == nil {
				result <- location
			}
			return err
		})
		failure <- err
	}()
	waitForLocationLock(t, ctx, pool)
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-failure:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("second transaction stayed blocked")
	}
	got := <-result
	if len(got.Categories) != 1 || got.Categories[0].ID != coffeeID {
		t.Fatalf("categories after lock wait = %+v", got.Categories)
	}
}

func waitForLocationLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1 FROM pg_stat_activity
			WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock'
			  AND query LIKE '%locations%' AND query LIKE '%FOR UPDATE%'
		)`).Scan(&blocked)
		if err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("second transaction did not wait on the Location lock")
}

func TestLocationMigrationUpDown(t *testing.T) {
	pool := locationfixture.SetupDatabase(t)
	ctx := context.Background()
	invalid := `INSERT INTO locations (name, is_supplier, building_id, coordinates, open_from, open_to)
		VALUES ('Invalid hours', false, $1, ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography, '24:00', '01:00')`
	if _, err := pool.Exec(ctx, invalid, com2ID); err == nil {
		t.Fatal("24:00 hours passed the constraint")
	}
	db, err := sql.Open("pgx", pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, file, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(file), "..", "..", "..", "database", "schema")
	if err := goose.DownTo(db, dir, 4); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, dir); err != nil {
		t.Fatalf("recreate Location schema: %v", err)
	}
	if _, err := pool.Exec(ctx, invalid, com2ID); err == nil {
		t.Fatal("24:00 hours passed after migration replay")
	}
	exec := func(statement string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, statement, args...); err != nil {
			t.Fatal(err)
		}
	}
	const (
		categoryOneID = "b526b558-e2ec-4db1-b873-13a9f490e07d"
		categoryTwoID = "29a7cb1e-44f2-4c68-825f-1ce39b78e44a"
		buildingID    = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"
		locationID    = "c0a3f4c4-12f0-4c17-aa44-8cdf6e76c94b"
	)
	exec(`
		INSERT INTO locations (
			id, name, is_supplier, building_id, floor, coordinates, revision
		) VALUES (
			$1, 'Current Supplier', TRUE, $2, 'B1',
			ST_SetSRID(ST_MakePoint(103.7742, 1.2942), 4326)::geography, 7
		)`, locationID, buildingID)
	exec(`
		INSERT INTO location_categories (location_id, category_id)
		VALUES ($1, $2), ($1, $3)`, locationID, categoryOneID, categoryTwoID)

	var (
		floor             string
		revision          int64
		relationshipCount int
	)
	if err := pool.QueryRow(ctx, `
		SELECT l.floor, l.revision, count(lc.category_id)
		FROM locations l
		JOIN location_categories lc ON lc.location_id = l.id
		WHERE l.id = $1
		GROUP BY l.id`, locationID).Scan(&floor, &revision, &relationshipCount); err != nil {
		t.Fatalf("read current Location schema: %v", err)
	}
	if floor != "B1" || revision != 7 || relationshipCount != 2 {
		t.Fatalf("current Location fields = floor %q, revision %d, Categories %d", floor, revision, relationshipCount)
	}

	var oldCategoryColumnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name = 'locations' AND column_name = 'category_id'
		)`).Scan(&oldCategoryColumnExists); err != nil {
		t.Fatalf("inspect Location columns: %v", err)
	}
	if oldCategoryColumnExists {
		t.Fatal("locations.category_id exists in the clean schema")
	}

	if _, err := pool.Exec(ctx, `UPDATE locations SET floor = ' ' WHERE id = $1`, locationID); err == nil {
		t.Fatal("blank floor passed the schema constraint")
	}
	if _, err := pool.Exec(ctx, `UPDATE locations SET revision = 0 WHERE id = $1`, locationID); err == nil {
		t.Fatal("non-positive revision passed the schema constraint")
	}
}

const (
	coopID   = "10000000-0000-4000-8000-000000000001"
	com2ID   = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"
	foodID   = "b526b558-e2ec-4db1-b873-13a9f490e07d"
	coffeeID = "29a7cb1e-44f2-4c68-825f-1ce39b78e44a"
)
