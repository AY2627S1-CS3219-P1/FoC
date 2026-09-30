//go:build integration

package location

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
)

func newTestAdminService(pool *pgxpool.Pool) *AdminService {
	return NewAdminService(NewPostgresAdminStore(pool), time.Now)
}

func adminInput() Input {
	return Input{
		Name: "New supplier", IsSupplier: true, CategoryIDs: []string{foodID},
		BuildingID: com2ID, Coordinates: &Coordinates{Latitude: 1.294, Longitude: 103.774},
		Details: "Pickup at counter",
	}
}

func TestPostgresAdminRoundTrip(t *testing.T) {
	pool := setupDatabase(t)
	ctx := context.Background()
	svc := newTestAdminService(pool)
	admin := Caller{ID: "admin", Admin: true}
	created, err := svc.Create(ctx, admin, CreateRequest{Key: uuid.NewString(), Input: adminInput()})
	if err != nil {
		t.Fatal(err)
	}
	if created.Revision != 1 || created.Name != "New supplier" || len(created.Categories) != 1 ||
		created.Categories[0].ID != foodID || created.Building.ID != com2ID {
		t.Fatalf("created = %+v", created)
	}
	updated, err := svc.Update(ctx, admin, UpdateRequest{
		ID: created.ID, ExpectedRevision: 1,
		Paths: []string{"name", "category_ids"},
		Input: Input{Name: "Updated supplier", CategoryIDs: []string{coffeeID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != 2 || updated.Name != "Updated supplier" || len(updated.Categories) != 1 || updated.Categories[0].ID != coffeeID {
		t.Fatalf("updated = %+v", updated)
	}
	if _, err := svc.Update(ctx, admin, UpdateRequest{ID: created.ID, ExpectedRevision: 1, Paths: []string{"name"}, Input: Input{Name: "stale"}}); !errors.Is(err, ErrAborted) {
		t.Fatalf("stale revision = %v", err)
	}
	archived, err := svc.Archive(ctx, admin, created.ID)
	if err != nil || archived.ArchivedAt == nil || archived.Revision != 3 {
		t.Fatalf("archive = %+v, %v", archived, err)
	}
	repeated, err := svc.Archive(ctx, admin, created.ID)
	if err != nil || repeated.Revision != archived.Revision || !repeated.UpdatedAt.Equal(archived.UpdatedAt) {
		t.Fatalf("repeat archive changed row = %+v, %v", repeated, err)
	}
	unarchived, err := svc.Unarchive(ctx, admin, created.ID)
	if err != nil || unarchived.ArchivedAt != nil || unarchived.Revision != 4 {
		t.Fatalf("unarchive = %+v, %v", unarchived, err)
	}
	repeated, err = svc.Unarchive(ctx, admin, created.ID)
	if err != nil || repeated.Revision != unarchived.Revision || !repeated.UpdatedAt.Equal(unarchived.UpdatedAt) {
		t.Fatalf("repeat unarchive changed row = %+v, %v", repeated, err)
	}
}

func TestPostgresAdminReferenceFailuresAndRollback(t *testing.T) {
	pool := setupDatabase(t)
	ctx := context.Background()
	svc := newTestAdminService(pool)
	admin := Caller{ID: "admin", Admin: true}
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
			if _, err := svc.Create(ctx, admin, CreateRequest{Key: uuid.NewString(), Input: input}); !errors.Is(err, tc.want) {
				t.Fatalf("create error = %v, want %v", err, tc.want)
			}
		})
	}
	// A valid scalar update followed by a Category FK failure must leave the
	// Location row and its old links unchanged.
	store := NewPostgresAdminStore(pool)
	err := store.Within(ctx, func(tx AdminTx) error {
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
	pool := setupDatabase(t)
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
	result := make(chan Location, 1)
	failure := make(chan error, 1)
	go func() {
		err := NewPostgresAdminStore(pool).Within(ctx, func(tx AdminTx) error {
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

func TestPostgresAdminConcurrentRevision(t *testing.T) {
	pool := setupDatabase(t)
	ctx := context.Background()
	first, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback(ctx) //nolint:errcheck
	if _, err := first.Exec(ctx, "SELECT id FROM locations WHERE id = $1 FOR UPDATE", coopID); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := newTestAdminService(pool).Update(ctx, Caller{ID: "admin", Admin: true}, UpdateRequest{
			ID: coopID, ExpectedRevision: 1, Paths: []string{"name"}, Input: Input{Name: "late write"},
		})
		result <- err
	}()
	waitForLocationLock(t, ctx, pool)
	if _, err := first.Exec(ctx, "UPDATE locations SET name = 'first write', revision = revision + 1 WHERE id = $1", coopID); err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, ErrAborted) {
			t.Fatalf("competing update = %v, want aborted", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("competing update stayed blocked")
	}
	var name string
	var revision int64
	if err := pool.QueryRow(ctx, "SELECT name, revision FROM locations WHERE id = $1", coopID).Scan(&name, &revision); err != nil {
		t.Fatal(err)
	}
	if name != "first write" || revision != 2 {
		t.Fatalf("location after competing updates = %q, revision %d", name, revision)
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
	pool := setupDatabase(t)
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
	dir := filepath.Join(filepath.Dir(file), "..", "..", "database", "schema")
	if err := goose.DownTo(db, dir, 4); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, dir); err != nil {
		t.Fatalf("recreate Location schema: %v", err)
	}
	if _, err := pool.Exec(ctx, invalid, com2ID); err == nil {
		t.Fatal("24:00 hours passed after migration replay")
	}
}
