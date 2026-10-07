//go:build integration

package admin

import (
	"context"
	"database/sql"
	sharedauth "github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	locationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	domainadmin "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/admin"
	location "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	discoveryrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/discovery"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/testsupport/rpcauth"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
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

	auth := rpcauth.New(t)
	path, handler := locationv1connect.NewLocationDiscoveryServiceHandler(
		discoveryrpc.NewLocationServer(location.NewService(location.NewPostgresReader(locationdb.New(pool)))),
		connect.WithInterceptors(sharedauth.RequireCaller(), validate.NewInterceptor()),
	)
	router := chi.NewRouter()
	router.Mount(path, auth.Authenticator.Authenticate(handler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	client := func(role string) locationv1connect.LocationDiscoveryServiceClient {
		return locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL, rpcauth.Bearer(auth.Token(t, role)))
	}
	ctx := context.Background()
	all := connect.NewRequest(&locationv1.ListLocationsRequest{
		StatusView: locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ALL,
	})

	res, err := client("user").ListLocations(ctx, connect.NewRequest(&locationv1.ListLocationsRequest{}))
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

func TestAdminAdminThroughSignedRPCAndDatabase(t *testing.T) {
	pool := startDatabase(t)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO buildings (id, name, center, radius_m) VALUES
			('a7ddb3ee-f24e-4464-bc33-6507ac5f5d68', 'COM2', ST_SetSRID(ST_MakePoint(103.774, 1.294), 4326)::geography, 75);
		INSERT INTO categories (id, name) VALUES
			('7b2b806c-14a1-4bda-ab01-e11d469fa213', 'Food');`); err != nil {
		t.Fatal(err)
	}
	auth := rpcauth.New(t)
	router := chi.NewRouter()
	locationAdmin := domainadmin.NewAdminService(domainadmin.NewPostgresAdminStore(pool), time.Now)
	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(NewServer(locationAdmin),
		connect.WithInterceptors(sharedauth.RequireAdmin(), validate.NewInterceptor()))
	router.Mount(adminPath, auth.Authenticator.Authenticate(adminHandler))
	reader := location.NewService(location.NewPostgresReader(locationdb.New(pool)))
	discoveryPath, discoveryHandler := locationv1connect.NewLocationDiscoveryServiceHandler(discoveryrpc.NewLocationServer(reader),
		connect.WithInterceptors(sharedauth.RequireCaller(), validate.NewInterceptor()))
	router.Mount(discoveryPath, auth.Authenticator.Authenticate(discoveryHandler))
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	admin := locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL, rpcauth.Bearer(auth.Token(t, "admin")))
	superAdmin := locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL, rpcauth.Bearer(auth.Token(t, "super_admin")))
	discovery := locationv1connect.NewLocationDiscoveryServiceClient(server.Client(), server.URL, rpcauth.Bearer(auth.Token(t, "admin")))
	ctx := context.Background()
	create := validCreateRequest()
	create.Location.Name = "  Cafe  "
	create.Location.IsSupplier = proto.Bool(true)
	create.Location.CategoryIds = []string{"7b2b806c-14a1-4bda-ab01-e11d469fa213"}
	create.Location.OpensAt = &timeofday.TimeOfDay{Hours: 22}
	create.Location.ClosesAt = &timeofday.TimeOfDay{Hours: 2}
	created, err := admin.CreateLocation(ctx, connect.NewRequest(create))
	if err != nil {
		t.Fatal(err)
	}
	id := created.Msg.Location.Id
	if id == "" || created.Msg.Location.Name != "Cafe" || created.Msg.Location.Revision != 1 {
		t.Fatalf("created=%v", created.Msg.Location)
	}

	// Replays return the resource as it exists now, including a later revision.
	retry, err := admin.CreateLocation(ctx, connect.NewRequest(create))
	if err != nil || retry.Msg.Location.Id != id {
		t.Fatalf("retry=%v err=%v", retry, err)
	}
	changed := proto.Clone(create).(*locationv1.CreateLocationRequest)
	changed.Location.Name = "Other"
	_, err = admin.CreateLocation(ctx, connect.NewRequest(changed))
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("key conflict=%v", err)
	}

	update := &locationv1.UpdateLocationRequest{Id: id, ExpectedRevision: 1,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor", "opens_at", "closes_at"}},
		Location:   &locationv1.LocationInput{Floor: proto.String(" B1 ")}}
	updated, err := superAdmin.UpdateLocation(ctx, connect.NewRequest(update))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Msg.Location.Revision != 2 || updated.Msg.Location.GetFloor() != "B1" || updated.Msg.Location.OpensAt != nil || updated.Msg.Location.ClosesAt != nil {
		t.Fatalf("updated=%v", updated.Msg.Location)
	}
	retry, err = admin.CreateLocation(ctx, connect.NewRequest(create))
	if err != nil || retry.Msg.Location.Revision != 2 {
		t.Fatalf("replay state=%v err=%v", retry, err)
	}
	_, err = admin.UpdateLocation(ctx, connect.NewRequest(update))
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("stale update=%v", err)
	}

	archived, err := admin.ArchiveLocation(ctx, connect.NewRequest(&locationv1.ArchiveLocationRequest{Id: id}))
	if err != nil || archived.Msg.Location.ArchivedAt == nil || archived.Msg.Location.Revision != 3 {
		t.Fatalf("archive=%v err=%v", archived, err)
	}
	archived, err = admin.ArchiveLocation(ctx, connect.NewRequest(&locationv1.ArchiveLocationRequest{Id: id}))
	if err != nil || archived.Msg.Location.Revision != 3 {
		t.Fatalf("archive retry=%v err=%v", archived, err)
	}
	active, err := discovery.ListLocations(ctx, connect.NewRequest(&locationv1.ListLocationsRequest{}))
	if err != nil || active.Msg.TotalItems != 0 {
		t.Fatalf("active=%v err=%v", active, err)
	}
	direct, err := discovery.GetLocation(ctx, connect.NewRequest(&locationv1.GetLocationRequest{Id: id}))
	if err != nil || direct.Msg.Location.ArchivedAt == nil {
		t.Fatalf("direct archived=%v err=%v", direct, err)
	}
	all, err := discovery.ListLocations(ctx, connect.NewRequest(&locationv1.ListLocationsRequest{StatusView: locationv1.LocationStatusView_LOCATION_STATUS_VIEW_ALL}))
	if err != nil || all.Msg.TotalItems != 1 {
		t.Fatalf("all=%v err=%v", all, err)
	}
	unarchived, err := superAdmin.UnarchiveLocation(ctx, connect.NewRequest(&locationv1.UnarchiveLocationRequest{Id: id}))
	if err != nil || unarchived.Msg.Location.ArchivedAt != nil || unarchived.Msg.Location.Revision != 4 {
		t.Fatalf("unarchive=%v err=%v", unarchived, err)
	}
	unarchived, err = admin.UnarchiveLocation(ctx, connect.NewRequest(&locationv1.UnarchiveLocationRequest{Id: id}))
	if err != nil || unarchived.Msg.Location.Revision != 4 {
		t.Fatalf("unarchive retry=%v err=%v", unarchived, err)
	}
	active, err = discovery.ListLocations(ctx, connect.NewRequest(&locationv1.ListLocationsRequest{}))
	if err != nil || active.Msg.TotalItems != 1 {
		t.Fatalf("active again=%v err=%v", active, err)
	}
	// Unmasked patch fields are ignored, even when they would be invalid if selected.
	unmasked := &locationv1.UpdateLocationRequest{Id: id, ExpectedRevision: 4,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}},
		Location:   &locationv1.LocationInput{Floor: proto.String("B2"), OpensAt: &timeofday.TimeOfDay{Hours: 8, Seconds: 3}}}
	ignored, err := admin.UpdateLocation(ctx, connect.NewRequest(unmasked))
	if err != nil || ignored.Msg.Location.Revision != 5 || ignored.Msg.Location.GetFloor() != "B2" || ignored.Msg.Location.OpensAt != nil {
		t.Fatalf("unmasked malformed field=%v err=%v", ignored, err)
	}
	direct, err = discovery.GetLocation(ctx, connect.NewRequest(&locationv1.GetLocationRequest{Id: id}))
	if err != nil || direct.Msg.Location.GetFloor() != "B2" || direct.Msg.Location.Revision != 5 {
		t.Fatalf("persisted unmasked update=%v err=%v", direct, err)
	}

	missingBuilding := proto.Clone(create).(*locationv1.CreateLocationRequest)
	missingBuilding.IdempotencyKey = "33a66319-daa7-4d4d-a91b-53dcb7de994e"
	missingBuilding.Location.BuildingId = "9a4eb4ca-a7c4-4f17-89a8-d83f80433624"
	_, err = admin.CreateLocation(ctx, connect.NewRequest(missingBuilding))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("missing building=%v", err)
	}
	noCategories := proto.Clone(create).(*locationv1.CreateLocationRequest)
	noCategories.IdempotencyKey = "e42daa75-03a0-469f-b04e-463706199249"
	noCategories.Location.CategoryIds = nil
	_, err = admin.CreateLocation(ctx, connect.NewRequest(noCategories))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("supplier without categories=%v", err)
	}

	// Exercise the authorization boundary with the live database-backed service.
	for _, role := range []string{"user", "suspended_user"} {
		denied := locationv1connect.NewLocationAdminServiceClient(server.Client(), server.URL, rpcauth.Bearer(auth.Token(t, role)))
		for name, call := range map[string]func() error{
			"create": func() error {
				_, err := denied.CreateLocation(ctx, connect.NewRequest(validCreateRequest()))
				return err
			},
			"update": func() error {
				_, err := denied.UpdateLocation(ctx, connect.NewRequest(unmasked))
				return err
			},
			"archive": func() error {
				_, err := denied.ArchiveLocation(ctx, connect.NewRequest(&locationv1.ArchiveLocationRequest{Id: id}))
				return err
			},
			"unarchive": func() error {
				_, err := denied.UnarchiveLocation(ctx, connect.NewRequest(&locationv1.UnarchiveLocationRequest{Id: id}))
				return err
			},
		} {
			if err := call(); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("%s %s = %v", role, name, err)
			}
		}
	}

	ordinary := validCreateRequest()
	ordinary.IdempotencyKey = "d973e239-f021-44cc-a51a-f43e70b4ca3f"
	// Duplicate names remain valid even with a different classification.
	ordinaryCreated, err := superAdmin.CreateLocation(ctx, connect.NewRequest(ordinary))
	if err != nil {
		t.Fatal(err)
	}
	if ordinaryCreated.Msg.Location.IsSupplier || len(ordinaryCreated.Msg.Location.Categories) != 0 || ordinaryCreated.Msg.Location.Id == id {
		t.Fatalf("ordinary create=%v", ordinaryCreated.Msg.Location)
	}
	classified, err := admin.UpdateLocation(ctx, connect.NewRequest(&locationv1.UpdateLocationRequest{
		Id: ordinaryCreated.Msg.Location.Id, ExpectedRevision: 1,
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_supplier", "category_ids"}},
		Location:   &locationv1.LocationInput{IsSupplier: proto.Bool(true), CategoryIds: create.Location.CategoryIds},
	}))
	if err != nil || !classified.Msg.Location.IsSupplier || len(classified.Msg.Location.Categories) != 1 || classified.Msg.Location.Revision != 2 {
		t.Fatalf("atomic classification=%v err=%v", classified, err)
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
	if err := goose.Up(db, filepath.Join(filepath.Dir(file), "..", "..", "..", "..", "database", "schema")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pool, err := pgxpool.New(context.Background(), databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
