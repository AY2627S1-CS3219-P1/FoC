//go:build integration

package seed

import (
	"context"
	"database/sql"
	"encoding/csv"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestLocationSchemaCreatesCurrentClassificationModel(t *testing.T) {
	databaseURL := startPostGIS(t)
	db := openSQLDB(t, databaseURL)
	migrateAll(t, db)
	pool := openPool(t, databaseURL)
	ctx := context.Background()

	const (
		categoryOneID = "b526b558-e2ec-4db1-b873-13a9f490e07d"
		categoryTwoID = "29a7cb1e-44f2-4c68-825f-1ce39b78e44a"
		buildingID    = "a7ddb3ee-f24e-4464-bc33-6507ac5f5d68"
		locationID    = "c0a3f4c4-12f0-4c17-aa44-8cdf6e76c94b"
	)
	execSQL(t, pool, `
		INSERT INTO categories (id, name)
		VALUES ($1, 'Food'), ($2, 'Coffee')`, categoryOneID, categoryTwoID)
	execSQL(t, pool, `
		INSERT INTO buildings (id, name, center, radius_m)
		VALUES ($1, 'COM2', ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography, 75)`, buildingID)
	execSQL(t, pool, `
		INSERT INTO locations (
			id, name, is_supplier, building_id, floor, coordinates, revision
		) VALUES (
			$1, 'Current Supplier', TRUE, $2, 'B1',
			ST_SetSRID(ST_MakePoint(103.7742, 1.2942), 4326)::geography, 7
		)`, locationID, buildingID)
	execSQL(t, pool, `
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

func TestSeedIsTransactionalRepeatableAndSpatiallyCorrect(t *testing.T) {
	databaseURL := startPostGIS(t)
	db := openSQLDB(t, databaseURL)
	migrateAll(t, db)
	pool := openPool(t, databaseURL)
	ctx := context.Background()

	paths := writeFixture(t, fixtureOptions{})
	first, err := Seed(ctx, pool, paths)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	wantFirst := Report{
		Buildings:          Counts{Inserted: 2},
		Categories:         Counts{Inserted: 2},
		SupplierLocations:  Counts{Inserted: 1},
		OrdinaryLocations:  Counts{Inserted: 1},
		LocationCategories: Counts{Inserted: 2},
	}
	if !reflect.DeepEqual(first, wantFirst) {
		t.Fatalf("first report = %#v, want %#v", first, wantFirst)
	}

	assertResourceCounts(t, pool, 2, 2, 2, 2)
	var (
		supplierID, supplierName, floor, openFrom, openTo string
		isSupplier                                        bool
		revision                                          int64
		longitude, latitude                               float64
	)
	err = pool.QueryRow(ctx, `
		SELECT id::text, name, is_supplier, floor, revision,
			ST_X(coordinates::geometry), ST_Y(coordinates::geometry),
			open_from::text, open_to::text
		FROM locations
		WHERE name = 'Night Coffee'`).Scan(
		&supplierID, &supplierName, &isSupplier, &floor, &revision,
		&longitude, &latitude, &openFrom, &openTo,
	)
	if err != nil {
		t.Fatalf("read seeded Supplier: %v", err)
	}
	if !isSupplier || floor != "B1" || revision != 1 {
		t.Fatalf("Supplier classification/floor/revision incorrect: supplier=%v floor=%q revision=%d", isSupplier, floor, revision)
	}
	if openFrom != "22:00:00" || openTo != "02:00:00" {
		t.Fatalf("overnight hours changed: %s-%s", openFrom, openTo)
	}
	if math.Abs(longitude-103.77421) > 0.0000001 || math.Abs(latitude-1.29421) > 0.0000001 {
		t.Fatalf("Supplier geography changed: longitude=%f latitude=%f", longitude, latitude)
	}

	var ordinaryCategoryCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM location_categories AS lc
		JOIN locations AS l ON l.id = lc.location_id
		WHERE NOT l.is_supplier`).Scan(&ordinaryCategoryCount); err != nil {
		t.Fatalf("count ordinary Location Categories: %v", err)
	}
	if ordinaryCategoryCount != 0 {
		t.Fatalf("ordinary Locations have %d Categories", ordinaryCategoryCount)
	}

	unchanged, err := Seed(ctx, pool, paths)
	if err != nil {
		t.Fatalf("unchanged seed rerun: %v", err)
	}
	if !reflect.DeepEqual(unchanged, Report{}) {
		t.Fatalf("unchanged rerun report = %#v, want no changes", unchanged)
	}
	if err := pool.QueryRow(ctx, `SELECT revision FROM locations WHERE id = $1`, supplierID).Scan(&revision); err != nil {
		t.Fatalf("read Supplier after unchanged rerun: %v", err)
	}
	if revision != 1 {
		t.Fatalf("unchanged rerun incremented revision to %d", revision)
	}

	paths = writeFixture(t, fixtureOptions{updatedSupplier: true})
	second, err := Seed(ctx, pool, paths)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	wantSecond := Report{
		SupplierLocations: Counts{Updated: 1},
	}
	if !reflect.DeepEqual(second, wantSecond) {
		t.Fatalf("second report = %#v, want %#v", second, wantSecond)
	}
	assertResourceCounts(t, pool, 2, 2, 2, 2)

	var updatedID, updatedDetails string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, name, details, revision
		FROM locations
		WHERE id = $1`, supplierID).Scan(&updatedID, &supplierName, &updatedDetails, &revision); err != nil {
		t.Fatalf("read updated Supplier: %v", err)
	}
	if updatedID != supplierID || supplierName != "Night Coffee Roastery" || updatedDetails != "Updated without changing identity" || revision != 2 {
		t.Fatalf("stable update failed: id=%s name=%q details=%q revision=%d", updatedID, supplierName, updatedDetails, revision)
	}

	invalidPaths := writeFixture(t, fixtureOptions{invalidOrdinaryLatitude: true})
	if _, err := Seed(ctx, pool, invalidPaths); err == nil || !strings.Contains(err.Error(), "Latitude must be between") {
		t.Fatalf("invalid seed error = %v", err)
	}
	assertResourceCounts(t, pool, 2, 2, 2, 2)
	if err := pool.QueryRow(ctx, `SELECT name, revision FROM locations WHERE id = $1`, supplierID).Scan(&supplierName, &revision); err != nil {
		t.Fatalf("read Supplier after rejected seed: %v", err)
	}
	if supplierName != "Night Coffee Roastery" || revision != 2 {
		t.Fatalf("invalid input wrote partial changes: name=%q revision=%d", supplierName, revision)
	}

	execSQL(t, pool, `
		CREATE FUNCTION reject_test_supplier() RETURNS trigger AS $$
		BEGIN
			IF NEW.name = 'Rejected Supplier' THEN
				RAISE EXCEPTION 'forced Location failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`)
	execSQL(t, pool, `
		CREATE TRIGGER reject_test_supplier
		BEFORE INSERT OR UPDATE ON locations
		FOR EACH ROW EXECUTE FUNCTION reject_test_supplier()`)

	rollbackPaths := writeFixture(t, fixtureOptions{updatedBuilding: true, rejectedSupplier: true})
	if _, err := Seed(ctx, pool, rollbackPaths); err == nil || !strings.Contains(err.Error(), "forced Location failure") {
		t.Fatalf("database-side seed error = %v", err)
	}
	var buildingName string
	if err := pool.QueryRow(ctx, `SELECT name FROM buildings WHERE id = $1`, deterministicUUID("building:central-library")).Scan(&buildingName); err != nil {
		t.Fatalf("read Building after rolled-back seed: %v", err)
	}
	if buildingName != "Central Library" {
		t.Fatalf("database failure did not roll back earlier Building update: name=%q", buildingName)
	}
	if err := pool.QueryRow(ctx, `SELECT name, revision FROM locations WHERE id = $1`, supplierID).Scan(&supplierName, &revision); err != nil {
		t.Fatalf("read Supplier after rolled-back seed: %v", err)
	}
	if supplierName != "Night Coffee Roastery" || revision != 2 {
		t.Fatalf("database failure left partial Location changes: name=%q revision=%d", supplierName, revision)
	}
}

func TestCommittedSeedDataImportsRepeatably(t *testing.T) {
	databaseURL := startPostGIS(t)
	db := openSQLDB(t, databaseURL)
	migrateAll(t, db)
	pool := openPool(t, databaseURL)
	ctx := context.Background()
	paths := committedSeedPaths(t)

	first, err := Seed(ctx, pool, paths)
	if err != nil {
		t.Fatalf("import committed seed data: %v", err)
	}
	if first.Buildings.Inserted == 0 || first.Categories.Inserted == 0 ||
		first.SupplierLocations.Inserted == 0 || first.OrdinaryLocations.Inserted == 0 ||
		first.LocationCategories.Inserted == 0 {
		t.Fatalf("committed seed report is missing a resource class: %#v", first)
	}
	second, err := Seed(ctx, pool, paths)
	if err != nil {
		t.Fatalf("rerun committed seed data: %v", err)
	}
	if !reflect.DeepEqual(second, Report{}) {
		t.Fatalf("committed seed rerun report = %#v, want no changes", second)
	}

	var supplierWithoutCategory, ordinaryWithCategory int
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE l.is_supplier AND NOT EXISTS (
				SELECT 1 FROM location_categories lc WHERE lc.location_id = l.id
			)),
			count(*) FILTER (WHERE NOT l.is_supplier AND EXISTS (
				SELECT 1 FROM location_categories lc WHERE lc.location_id = l.id
			))
		FROM locations l`).Scan(&supplierWithoutCategory, &ordinaryWithCategory); err != nil {
		t.Fatalf("read committed classification invariants: %v", err)
	}
	if supplierWithoutCategory != 0 || ordinaryWithCategory != 0 {
		t.Fatalf("committed classification invariants failed: supplier_without=%d ordinary_with=%d", supplierWithoutCategory, ordinaryWithCategory)
	}

	var ordinaryMatches int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM locations
		WHERE NOT is_supplier AND name IN (
			'Central Library Main Entrance', 'COM2 Foyer', 'Yusof Ishak House Foyer'
		)`).Scan(&ordinaryMatches); err != nil {
		t.Fatalf("read committed ordinary Locations: %v", err)
	}
	if ordinaryMatches != 3 {
		t.Fatalf("found %d of the three required ordinary Locations", ordinaryMatches)
	}

	var categoryCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM location_categories lc
		JOIN locations l ON l.id = lc.location_id
		WHERE l.name = 'Cafe+ Robot Cafe'`).Scan(&categoryCount); err != nil {
		t.Fatalf("read committed combined Categories: %v", err)
	}
	if categoryCount != 2 {
		t.Fatalf("combined Food/Coffee produced %d Category relationships", categoryCount)
	}

	expectedCoordinates := []struct {
		name      string
		latitude  float64
		longitude float64
	}{
		{name: "Central Library Main Entrance", latitude: 1.2966455, longitude: 103.7732263},
		{name: "COM2 Foyer", latitude: 1.293655617, longitude: 103.774299300},
		{name: "Yusof Ishak House Foyer", latitude: 1.298410586, longitude: 103.774553900},
	}
	for _, expected := range expectedCoordinates {
		var longitude, latitude float64
		if err := pool.QueryRow(ctx, `
			SELECT ST_X(coordinates::geometry), ST_Y(coordinates::geometry)
			FROM locations WHERE name = $1`, expected.name).Scan(&longitude, &latitude); err != nil {
			t.Fatalf("read committed %s coordinates: %v", expected.name, err)
		}
		if math.Abs(longitude-expected.longitude) > 0.0000001 || math.Abs(latitude-expected.latitude) > 0.0000001 {
			t.Fatalf("%s coordinates changed: longitude=%f latitude=%f", expected.name, longitude, latitude)
		}
	}
}

type fixtureOptions struct {
	updatedSupplier         bool
	invalidOrdinaryLatitude bool
	updatedBuilding         bool
	rejectedSupplier        bool
}

func writeFixture(t *testing.T, options fixtureOptions) Paths {
	t.Helper()
	directory := t.TempDir()
	buildingsPath := filepath.Join(directory, "buildings.csv")
	suppliersPath := filepath.Join(directory, "suppliers.csv")
	locationsPath := filepath.Join(directory, "locations.csv")

	centralLibraryName := "Central Library"
	if options.updatedBuilding {
		centralLibraryName = "Central Library Updated"
	}
	writeCSV(t, buildingsPath, append([][]string{buildingHeaders}, [][]string{
		{"building:central-library", centralLibraryName, "CLB", "1.2965", "103.7731", "100"},
		{"building:com2", "COM2", "Com 2|Com2", "1.2940", "103.7740", "100"},
	}...))

	supplierName := "Night Coffee"
	details := "Open overnight"
	if options.updatedSupplier {
		supplierName = "Night Coffee Roastery"
		details = "Updated without changing identity"
	}
	if options.rejectedSupplier {
		supplierName = "Rejected Supplier"
		details = "This valid row fails inside the transaction"
	}
	writeCSV(t, suppliersPath, append([][]string{supplierHeaders}, []string{
		"supplier:night-coffee", supplierName, "Food/Coffee", "Com 2", "B1", details,
		"1.29421", "103.77421", "2200hrs", "0200hrs", "https://ignored.invalid/image.jpg",
	}))

	ordinaryLatitude := "1.29644"
	if options.invalidOrdinaryLatitude {
		ordinaryLatitude = "95"
	}
	writeCSV(t, locationsPath, append([][]string{ordinaryLocationHeaders}, []string{
		"location:central-library-main-entrance", "Central Library Main Entrance",
		"building:central-library", "1", "Main entrance", ordinaryLatitude, "103.77303", "", "",
	}))

	return Paths{Suppliers: suppliersPath, Buildings: buildingsPath, OrdinaryLocations: locationsPath}
}

func committedSeedPaths(t *testing.T) Paths {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate committed seed data")
	}
	repositoryRoot := filepath.Join(filepath.Dir(file), "..", "..", "..")
	return Paths{
		Suppliers:         filepath.Join(repositoryRoot, "data", "csv", "supplier-seed-data.csv"),
		Buildings:         filepath.Join(repositoryRoot, "data", "csv", "building-seed-data.csv"),
		OrdinaryLocations: filepath.Join(repositoryRoot, "data", "csv", "location-seed-data.csv"),
	}
}

func writeCSV(t *testing.T, path string, records [][]string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture %s: %v", path, err)
	}
	writer := csv.NewWriter(file)
	if err := writer.WriteAll(records); err != nil {
		_ = file.Close()
		t.Fatalf("write fixture %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close fixture %s: %v", path, err)
	}
}

func startPostGIS(t *testing.T) string {
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
	defer testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start PostGIS: %v", err)
	}
	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostGIS connection string: %v", err)
	}
	return databaseURL
}

func openSQLDB(t *testing.T, databaseURL string) *sql.DB {
	t.Helper()
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Ping(); err != nil {
		t.Fatalf("ping migration database: %v", err)
	}
	return db
}

func openPool(t *testing.T, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open pgx pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func migrateAll(t *testing.T, db *sql.DB) {
	t.Helper()
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("set Goose dialect: %v", err)
	}
	if err := goose.Up(db, migrationDirectory(t)); err != nil {
		t.Fatalf("migrate clean database: %v", err)
	}
}

func migrationDirectory(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "database", "schema")
}

func execSQL(t *testing.T, pool *pgxpool.Pool, statement string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), statement, args...); err != nil {
		t.Fatalf("execute test SQL: %v", err)
	}
}

func assertResourceCounts(t *testing.T, pool *pgxpool.Pool, buildings, categories, locations, relationships int) {
	t.Helper()
	ctx := context.Background()
	queries := []struct {
		name string
		sql  string
		want int
	}{
		{"buildings", "SELECT count(*) FROM buildings", buildings},
		{"categories", "SELECT count(*) FROM categories", categories},
		{"locations", "SELECT count(*) FROM locations", locations},
		{"location_categories", "SELECT count(*) FROM location_categories", relationships},
	}
	for _, query := range queries {
		var got int
		if err := pool.QueryRow(ctx, query.sql).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", query.name, err)
		}
		if got != query.want {
			t.Fatalf("%s count = %d, want %d", query.name, got, query.want)
		}
	}
}
