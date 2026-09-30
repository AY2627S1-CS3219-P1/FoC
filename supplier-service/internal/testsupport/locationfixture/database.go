//go:build integration

package locationfixture

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func SetupDatabase(t *testing.T) *pgxpool.Pool {
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
	if err := goose.Up(db, filepath.Join(filepath.Dir(file), "..", "..", "..", "database", "schema")); err != nil {
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
			('10000000-0000-4000-8000-000000000001', now() - interval '1 hour', NULL, now() - interval '2 hours', 'Cancelled', 'admin');
		INSERT INTO location_disablements (location_id, starts_at, created_at, reason, created_by) VALUES
			('10000000-0000-4000-8000-000000000003', date_trunc('hour', now()) - interval '1 hour', now() - interval '1 minute', 'Older', 'admin'),
			('10000000-0000-4000-8000-000000000003', date_trunc('hour', now()) - interval '1 hour', now(), 'Newer', 'admin');`); err != nil {
		t.Fatalf("insert fixtures: %v", err)
	}
	return pool
}
