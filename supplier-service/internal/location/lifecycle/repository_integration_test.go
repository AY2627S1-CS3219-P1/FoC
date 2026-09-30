//go:build integration

package lifecycle_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	workflowrepo "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	workflows "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	postgresBuildingID = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	postgresCategoryID = "63dc656d-4e7b-43ad-a0ba-325b44132766"
	otherCategoryID    = "d064dc96-f8be-47ef-a1a3-9c72c05e708c"
	postgresLocationID = "8f383cca-1609-4326-8440-f75d943dc924"
)

type fixture struct {
	ctx        context.Context
	pool       *pgxpool.Pool
	db         *sql.DB
	migrations string
}

func TestWorkflowPersistencePostGIS(t *testing.T) {
	f := newFixture(t)
	t.Run("Location links are read after a row lock wait", func(t *testing.T) {
		f.reset(t)
		f.exec(t, "INSERT INTO location_categories(location_id,category_id) VALUES($1,$2)", postgresLocationID, postgresCategoryID)
		tx, err := f.pool.Begin(f.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(f.ctx)
		for _, statement := range []string{
			"UPDATE locations SET name='Changed Location',is_supplier=true,revision=revision+1 WHERE id=$1",
			"DELETE FROM location_categories WHERE location_id=$1",
		} {
			if _, err := tx.Exec(f.ctx, statement, postgresLocationID); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := tx.Exec(f.ctx, "INSERT INTO location_categories(location_id,category_id) VALUES($1,$2)", postgresLocationID, otherCategoryID); err != nil {
			t.Fatal(err)
		}
		result := make(chan workflows.Location, 1)
		errors := make(chan error, 1)
		go func() {
			var got workflows.Location
			err := workflowrepo.NewPostgresRepository(f.pool, time.Now).Within(f.ctx, func(tx workflows.Tx) error {
				var err error
				got, err = tx.Location(f.ctx, postgresLocationID)
				return err
			})
			result <- got
			errors <- err
		}()
		waitForLocationLock(t, f)
		if err := tx.Commit(f.ctx); err != nil {
			t.Fatal(err)
		}
		got := <-result
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if got.Proposal.Name != "Changed Location" || len(got.Proposal.CategoryIDs) != 1 || got.Proposal.CategoryIDs[0] != otherCategoryID || len(got.Categories) != 1 || got.Categories[0].ID != otherCategoryID {
			t.Fatalf("Location used inconsistent row and links: %+v", got)
		}
	})

	t.Run("downgrade refuses to discard a pending request floor", func(t *testing.T) {
		f.reset(t)
		id := uuid.NewString()
		f.exec(t, "INSERT INTO location_addition_requests(id,submitted_by,name,building_id,coordinates,floor) VALUES($1,'owner','Bench',$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography,'2')", id, postgresBuildingID)
		if err := goose.Down(f.db, f.migrations); err == nil {
			t.Fatal("downgrade discarded request floor")
		}
		var floor, status string
		if err := f.pool.QueryRow(f.ctx, "SELECT floor,status FROM location_addition_requests WHERE id=$1", id).Scan(&floor, &status); err != nil || floor != "2" || status != "pending" {
			t.Fatalf("request changed after refused downgrade: floor=%q status=%q error=%v", floor, status, err)
		}
		if version, err := goose.GetDBVersion(f.db); err != nil || version != 10 {
			t.Fatalf("migration version=%d error=%v", version, err)
		}
	})

	t.Run("downgrade preserves an incomplete legacy proposal", func(t *testing.T) {
		f.reset(t)
		if err := goose.Down(f.db, f.migrations); err != nil {
			t.Fatal(err)
		}
		id := uuid.NewString()
		f.exec(t, "INSERT INTO location_addition_requests(id,submitted_by,name,is_supplier,category_id,building_id) VALUES($1,'owner','Legacy Cafe',true,$2,$3)", id, postgresCategoryID, postgresBuildingID)
		if err := goose.Up(f.db, f.migrations); err != nil {
			t.Fatal(err)
		}
		if err := goose.Down(f.db, f.migrations); err != nil {
			t.Fatal(err)
		}
		var category, status string
		var missingCoordinates bool
		if err := f.pool.QueryRow(f.ctx, "SELECT category_id::text,status,coordinates IS NULL FROM location_addition_requests WHERE id=$1", id).Scan(&category, &status, &missingCoordinates); err != nil || category != postgresCategoryID || status != "pending" || !missingCoordinates {
			t.Fatalf("legacy proposal changed: category=%q status=%q missing=%v error=%v", category, status, missingCoordinates, err)
		}
		if err := goose.Up(f.db, f.migrations); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM location_addition_request_categories WHERE request_id=$1 AND category_id=$2", id, postgresCategoryID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("restored Category links=%d error=%v", count, err)
		}
	})

	t.Run("downgrade preserves a non supplier request without Categories", func(t *testing.T) {
		f.reset(t)
		id := uuid.NewString()
		f.exec(t, "INSERT INTO location_addition_requests(id,submitted_by,name,building_id,coordinates) VALUES($1,'owner','Bench',$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography)", id, postgresBuildingID)
		if err := goose.Down(f.db, f.migrations); err != nil {
			t.Fatal(err)
		}
		var name string
		var missingCategory bool
		if err := f.pool.QueryRow(f.ctx, "SELECT name,category_id IS NULL FROM location_addition_requests WHERE id=$1", id).Scan(&name, &missingCategory); err != nil || name != "Bench" || !missingCategory {
			t.Fatalf("request changed: name=%q missingCategory=%v error=%v", name, missingCategory, err)
		}
		if err := goose.Up(f.db, f.migrations); err != nil {
			t.Fatal(err)
		}
	})
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	options := []testcontainers.ContainerCustomizer{postgres.WithDatabase("workflow_test"), postgres.WithUsername("supplier"), postgres.WithPassword("supplier"), postgres.BasicWaitStrategies()}
	if runtime.GOARCH == "arm64" {
		options = append(options, testcontainers.WithImagePlatform("linux/amd64"))
	}
	container, err := postgres.Run(ctx, "postgis/postgis:18-3.6", options...)
	if err != nil {
		t.Fatal(err)
	}
	testcontainers.CleanupContainer(t, container)
	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, file, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(file), "../../../database/schema")
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, migrations); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &fixture{ctx: ctx, pool: pool, db: db, migrations: migrations}
}

func (f *fixture) exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, statement, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) reset(t *testing.T) {
	t.Helper()
	f.exec(t, "TRUNCATE supplier_idempotency,location_addition_requests,locations,buildings,categories CASCADE")
	f.exec(t, "INSERT INTO buildings(id,name,center,radius_m) VALUES($1,'COM2',ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography,75)", postgresBuildingID)
	f.exec(t, "INSERT INTO categories(id,name) VALUES($1,'Food'),($2,'Coffee')", postgresCategoryID, otherCategoryID)
	f.exec(t, "INSERT INTO locations(id,name,is_supplier,building_id,coordinates) VALUES($1,'Existing Location',true,$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography)", postgresLocationID, postgresBuildingID)
}

func waitForLocationLock(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var blocked bool
		if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM locations l%')").Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Location read did not wait behind the Category edit")
}
