//go:build integration

package rpc

import (
	"context"
	"database/sql"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	supplierv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestSignedTokenReachesDatabase sends signed tokens through the auth
// middleware, the Location handler and service, and a real PostGIS database.
func TestSignedTokenReachesDatabase(t *testing.T) {
	pool := startDatabase(t)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO buildings (id, name, center, radius_m) VALUES
			('a7ddb3ee-f24e-4464-bc33-6507ac5f5d68', 'COM2', ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography, 75);
		INSERT INTO locations (id, name, building_id, coordinates, archived_at) VALUES
			('10000000-0000-4000-8000-000000000001', 'Active', 'a7ddb3ee-f24e-4464-bc33-6507ac5f5d68',
				ST_SetSRID(ST_MakePoint(103.7741, 1.2941), 4326)::geography, NULL),
			('10000000-0000-4000-8000-000000000002', 'Archived', 'a7ddb3ee-f24e-4464-bc33-6507ac5f5d68',
				ST_SetSRID(ST_MakePoint(103.7742, 1.2942), 4326)::geography, now());`); err != nil {
		t.Fatalf("insert fixtures: %v", err)
	}

	auth := newTestAuth(t)
	path, handler := supplierv1connect.NewLocationServiceHandler(
		NewLocationServer(location.NewService(location.NewPostgresReader(locationdb.New(pool)))),
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	router := chi.NewRouter()
	router.Mount(path, auth.authenticator.Authenticate(handler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := func(role string) supplierv1connect.LocationServiceClient {
		return supplierv1connect.NewLocationServiceClient(server.Client(), server.URL, bearer(auth.token(t, role)))
	}
	ctx := context.Background()
	all := connect.NewRequest(&supplierv1.ListLocationsRequest{
		StatusView: supplierv1.LocationStatusView_LOCATION_STATUS_VIEW_ALL,
	})

	res, err := client("user").ListLocations(ctx, connect.NewRequest(&supplierv1.ListLocationsRequest{}))
	if err != nil || res.Msg.TotalItems != 1 || res.Msg.Locations[0].Name != "Active" {
		t.Fatalf("user list = %v, err %v", res, err)
	}
	if _, err := client("user").ListLocations(ctx, all); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("user all view: err = %v, want permission denied", err)
	}
	res, err = client("admin").ListLocations(ctx, all)
	if err != nil || res.Msg.TotalItems != 2 {
		t.Fatalf("admin list = %v, err %v", res, err)
	}
}

func startDatabase(t *testing.T) *pgxpool.Pool {
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
	return pool
}
