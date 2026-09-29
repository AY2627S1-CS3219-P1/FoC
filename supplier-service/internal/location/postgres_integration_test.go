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

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	com2ID   = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"
	pgpID    = "b1ddb3ee-f24e-4464-bc33-6507ac5f5d68"
	foodID   = "b526b558-e2ec-4db1-b873-13a9f490e07d"
	coffeeID = "29a7cb1e-44f2-4c68-825f-1ce39b78e44a"

	coopID     = "10000000-0000-4000-8000-000000000001"
	supperID   = "10000000-0000-4000-8000-000000000002"
	cafeID     = "10000000-0000-4000-8000-000000000003"
	loungeID   = "10000000-0000-4000-8000-000000000004"
	archivedID = "10000000-0000-4000-8000-000000000005"
)

func TestPostgresReader(t *testing.T) {
	pool := setupDatabase(t)
	service := NewService(NewPostgresReader(locationdb.New(pool)))
	ctx := context.Background()
	user, admin := Caller{}, Caller{Admin: true}

	t.Run("get returns joined details", func(t *testing.T) {
		loc, err := service.Get(ctx, supperID)
		if err != nil {
			t.Fatal(err)
		}
		if loc.Name != "Supper Stretch" || loc.Building.Name != "PGP" || !loc.IsSupplier ||
			len(loc.Categories) != 2 || loc.Categories[0].Name != "Coffee" ||
			loc.OpensAt == nil || *loc.OpensAt != "22:00" || *loc.ClosesAt != "02:00" ||
			loc.Coordinates.Latitude != 1.2915 || loc.Coordinates.Longitude != 103.7805 ||
			loc.CurrentDisablement == nil || loc.CurrentDisablement.Reason != "Renovation" {
			t.Fatalf("location = %+v", loc)
		}
	})

	t.Run("get returns archived and reports missing", func(t *testing.T) {
		loc, err := service.Get(ctx, archivedID)
		if err != nil || loc.ArchivedAt == nil {
			t.Fatalf("archived location = %+v, err %v", loc, err)
		}
		if _, err := service.Get(ctx, "20000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("missing: err = %v", err)
		}
	})

	t.Run("expired and cancelled disablements are not current", func(t *testing.T) {
		loc, err := service.Get(ctx, coopID)
		if err != nil || loc.CurrentDisablement != nil {
			t.Fatalf("location = %+v, err %v", loc, err)
		}
	})

	list := func(t *testing.T, caller Caller, req ListRequest) Page {
		t.Helper()
		page, err := service.List(ctx, caller, req)
		if err != nil {
			t.Fatal(err)
		}
		return page
	}

	cases := []struct {
		name   string
		caller Caller
		req    ListRequest
		want   []string
	}{
		{"default excludes archived, name ascending", user, ListRequest{}, []string{"Lounge", "NUS Co-op", "Supper Stretch", "The Cafe"}},
		{"name descending", user, ListRequest{Descending: true}, []string{"The Cafe", "Supper Stretch", "NUS Co-op", "Lounge"}},
		{"building ascending then name", user, ListRequest{Sort: SortByBuilding}, []string{"Lounge", "NUS Co-op", "Supper Stretch", "The Cafe"}},
		{"building descending", user, ListRequest{Sort: SortByBuilding, Descending: true}, []string{"The Cafe", "Supper Stretch", "NUS Co-op", "Lounge"}},
		{"case-insensitive substring", user, ListRequest{Search: "co-OP"}, []string{"NUS Co-op"}},
		{"fuzzy typo", user, ListRequest{Search: "supper strech"}, []string{"Supper Stretch"}},
		{"wildcards match literally", user, ListRequest{Search: "%"}, nil},
		{"building filter", user, ListRequest{BuildingID: ptr(pgpID)}, []string{"Supper Stretch", "The Cafe"}},
		{"category filter", user, ListRequest{CategoryID: ptr(coffeeID)}, []string{"Supper Stretch", "The Cafe"}},
		{"suppliers only", user, ListRequest{SuppliersOnly: true}, []string{"NUS Co-op", "Supper Stretch", "The Cafe"}},
		{"archived only", admin, ListRequest{Archive: ArchiveArchived}, []string{"Old Stall"}},
		{"all", admin, ListRequest{Archive: ArchiveAll}, []string{"Lounge", "NUS Co-op", "Old Stall", "Supper Stretch", "The Cafe"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := list(t, tc.caller, tc.req)
			if got := names(page.Locations); !equal(got, tc.want) {
				t.Fatalf("names = %v, want %v", got, tc.want)
			}
			if page.TotalItems != int64(len(tc.want)) {
				t.Fatalf("total = %d, want %d", page.TotalItems, len(tc.want))
			}
		})
	}

	t.Run("pages are stable and complete", func(t *testing.T) {
		var got []string
		for p := int32(1); p <= 3; p++ {
			page := list(t, user, ListRequest{Page: p, PageSize: 2})
			if page.TotalPages != 2 {
				t.Fatalf("total pages = %d, want 2", page.TotalPages)
			}
			got = append(got, names(page.Locations)...)
		}
		want := []string{"Lounge", "NUS Co-op", "Supper Stretch", "The Cafe"}
		if !equal(got, want) {
			t.Fatalf("pages = %v, want %v", got, want)
		}
	})

	t.Run("reference data", func(t *testing.T) {
		buildings, err := service.ListBuildings(ctx)
		if err != nil || len(buildings) != 2 || buildings[0].Name != "COM2" || buildings[0].Center.Longitude != 103.774 {
			t.Fatalf("buildings = %+v, err %v", buildings, err)
		}
		categories, err := service.ListCategories(ctx)
		if err != nil || len(categories) != 2 || categories[0].Name != "Coffee" {
			t.Fatalf("categories = %+v, err %v", categories, err)
		}
	})
}

func setupDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	options := []testcontainers.ContainerCustomizer{
		postgres.WithDatabase("supplier_test"),
		postgres.WithUsername("supplier"),
		postgres.WithPassword("supplier"),
		postgres.BasicWaitStrategies(),
	}
	if runtime.GOARCH == "arm64" {
		options = append(options, testcontainers.WithImagePlatform("linux/amd64"))
	}
	container, err := postgres.Run(ctx, "postgis/postgis:18-3.6", options...)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start PostGIS: %v", err)
	}
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	defer db.Close()
	_, file, _, _ := runtime.Caller(0)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, filepath.Join(filepath.Dir(file), "..", "..", "database", "schema")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	// Multiple statements need the simple protocol, so IDs are inlined.
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO buildings (id, name, center, radius_m) VALUES
			('a7ddb3ee-f24e-4464-bc33-6507ac5f5d68', 'COM2', ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography, 75),
			('b1ddb3ee-f24e-4464-bc33-6507ac5f5d68', 'PGP', ST_SetSRID(ST_MakePoint(103.780, 1.291), 4326)::geography, 120);
		INSERT INTO categories (id, name) VALUES ('b526b558-e2ec-4db1-b873-13a9f490e07d', 'Food'), ('29a7cb1e-44f2-4c68-825f-1ce39b78e44a', 'Coffee');
		INSERT INTO locations (id, name, is_supplier, building_id, coordinates, open_from, open_to, archived_at) VALUES
			('10000000-0000-4000-8000-000000000001', 'NUS Co-op', TRUE, 'a7ddb3ee-f24e-4464-bc33-6507ac5f5d68', ST_SetSRID(ST_MakePoint(103.7741, 1.2941), 4326)::geography, NULL, NULL, NULL),
			('10000000-0000-4000-8000-000000000002', 'Supper Stretch', TRUE, 'b1ddb3ee-f24e-4464-bc33-6507ac5f5d68', ST_SetSRID(ST_MakePoint(103.7805, 1.2915), 4326)::geography, '22:00', '02:00', NULL),
			('10000000-0000-4000-8000-000000000003', 'The Cafe', TRUE, 'b1ddb3ee-f24e-4464-bc33-6507ac5f5d68', ST_SetSRID(ST_MakePoint(103.7806, 1.2916), 4326)::geography, NULL, NULL, NULL),
			('10000000-0000-4000-8000-000000000004', 'Lounge', FALSE, 'a7ddb3ee-f24e-4464-bc33-6507ac5f5d68', ST_SetSRID(ST_MakePoint(103.7742, 1.2942), 4326)::geography, NULL, NULL, NULL),
			('10000000-0000-4000-8000-000000000005', 'Old Stall', TRUE, 'a7ddb3ee-f24e-4464-bc33-6507ac5f5d68', ST_SetSRID(ST_MakePoint(103.7743, 1.2943), 4326)::geography, NULL, NULL, now());
		INSERT INTO location_categories (location_id, category_id) VALUES
			('10000000-0000-4000-8000-000000000001', 'b526b558-e2ec-4db1-b873-13a9f490e07d'), ('10000000-0000-4000-8000-000000000002', 'b526b558-e2ec-4db1-b873-13a9f490e07d'), ('10000000-0000-4000-8000-000000000002', '29a7cb1e-44f2-4c68-825f-1ce39b78e44a'), ('10000000-0000-4000-8000-000000000003', '29a7cb1e-44f2-4c68-825f-1ce39b78e44a'), ('10000000-0000-4000-8000-000000000005', 'b526b558-e2ec-4db1-b873-13a9f490e07d');
		INSERT INTO location_disablements (location_id, starts_at, ends_at, cancelled_at, reason, created_by) VALUES
			('10000000-0000-4000-8000-000000000002', now() - interval '1 hour', NULL, NULL, 'Renovation', 'admin'),
			('10000000-0000-4000-8000-000000000001', now() - interval '2 days', now() - interval '1 day', NULL, 'Expired', 'admin'),
			('10000000-0000-4000-8000-000000000001', now() - interval '1 hour', NULL, now(), 'Cancelled', 'admin');`); err != nil {
		t.Fatalf("insert fixtures: %v", err)
	}
	return pool
}

func names(locations []Location) []string {
	var out []string
	for _, loc := range locations {
		out = append(out, loc.Name)
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func ptr(s string) *string { return &s }
