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
	goose "github.com/pressly/goose/v3"
	testcontainers "github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const (
	postgresBuildingID = "9dbda827-5a46-4089-9209-a2bde43f18a0"
	postgresCategoryID = "63dc656d-4e7b-43ad-a0ba-325b44132766"
	otherCategoryID    = "d064dc96-f8be-47ef-a1a3-9c72c05e708c"
	postgresLocationID = "8f383cca-1609-4326-8440-f75d943dc924"
)

type WorkflowDatabase struct {
	Context    context.Context
	Pool       *pgxpool.Pool
	DB         *sql.DB
	Migrations string
}

func NewWorkflowDatabase(t *testing.T) *WorkflowDatabase {
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
	return &WorkflowDatabase{Context: ctx, Pool: pool, DB: db, Migrations: migrations}
}

func (f *WorkflowDatabase) Exec(t *testing.T, statement string, args ...any) {
	t.Helper()
	if _, err := f.Pool.Exec(f.Context, statement, args...); err != nil {
		t.Fatal(err)
	}
}

func (f *WorkflowDatabase) Reset(t *testing.T) {
	t.Helper()
	f.Exec(t, "TRUNCATE supplier_idempotency,location_addition_requests,locations,buildings,categories CASCADE")
	f.Exec(t, "INSERT INTO buildings(id,name,center,radius_m) VALUES($1,'COM2',ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography,75)", postgresBuildingID)
	f.Exec(t, "INSERT INTO categories(id,name) VALUES($1,'Food'),($2,'Coffee')", postgresCategoryID, otherCategoryID)
	f.Exec(t, "INSERT INTO locations(id,name,is_supplier,building_id,coordinates) VALUES($1,'Existing Location',true,$2,ST_SetSRID(ST_MakePoint(103.774,1.294),4326)::geography)", postgresLocationID, postgresBuildingID)
}
