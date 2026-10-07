//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1"
	rpc "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/idempotency"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/genproto/googleapis/type/timeofday"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// proveAdminIntegration uses the production server and a disposable PostGIS
// database. Faults and lock barriers are confined to that database.
func proveAdminIntegration(t *testing.T, pool *pgxpool.Pool, admin rpc.LocationAdminServiceClient, discovery rpc.LocationDiscoveryServiceClient, disablement rpc.LocationDisablementServiceClient, input *pb.LocationInput) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	newCreate := func(name string) *pb.CreateLocationRequest {
		in := proto.Clone(input).(*pb.LocationInput)
		in.Name = name
		in.CategoryIds = []string{"b526b558-e2ec-4db1-b873-13a9f490e07d", "29a7cb1e-44f2-4c68-825f-1ce39b78e44a"}
		return &pb.CreateLocationRequest{Location: in, IdempotencyKey: uuid.NewString()}
	}
	create := func(t *testing.T, name string) (*pb.CreateLocationRequest, *pb.Location) {
		t.Helper()
		request := newCreate(name)
		response, err := admin.CreateLocation(ctx, connect.NewRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		return request, response.Msg.Location
	}
	t.Run("concurrent normalized Create retries commit one identity", func(t *testing.T) {
		request := newCreate("Concurrent proof")
		barrier, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer barrier.Rollback(context.Background()) //nolint:errcheck
		if err := idempotency.NewPostgresStore(barrier).Lock(ctx, idempotency.Scope{Caller: "user-1", Method: "CreateLocation", Key: request.IdempotencyKey}); err != nil {
			t.Fatal(err)
		}
		var pid int
		if err := barrier.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		type result struct {
			location *pb.Location
			err      error
		}
		results := make(chan result, 8)
		start := make(chan struct{})
		for i := 0; i < 8; i++ {
			in := proto.Clone(request).(*pb.CreateLocationRequest)
			if i%2 == 0 {
				in.Location.Name = "  Concurrent proof  "
				in.Location.BuildingId = strings.ToUpper(in.Location.BuildingId)
				in.Location.CategoryIds[0], in.Location.CategoryIds[1] = in.Location.CategoryIds[1], in.Location.CategoryIds[0]
			}
			go func() {
				<-start
				response, err := admin.CreateLocation(ctx, connect.NewRequest(in))
				var loc *pb.Location
				if response != nil {
					loc = response.Msg.Location
				}
				results <- result{loc, err}
			}()
		}
		close(start)
		waitForDatabaseBlock(t, ctx, pool, pid, "%pg_advisory_xact_lock%", 8)
		if err := barrier.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		var id string
		for i := 0; i < 8; i++ {
			r := <-results
			if r.err != nil {
				t.Fatal(r.err)
			}
			if id == "" {
				id = r.location.Id
			}
			if r.location.Id != id || r.location.Revision != 1 || len(r.location.Categories) != 2 {
				t.Fatalf("retry identity/state: %v", r.location)
			}
		}
		var locations, links, records int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM locations WHERE name=$1), (SELECT count(*) FROM location_categories WHERE location_id=$2), (SELECT count(*) FROM supplier_idempotency WHERE key=$3 AND resource_id=$2)`, "Concurrent proof", id, request.IdempotencyKey).Scan(&locations, &links, &records); err != nil {
			t.Fatal(err)
		}
		if locations != 1 || links != 2 || records != 1 {
			t.Fatalf("rows: locations=%d links=%d records=%d", locations, links, records)
		}
		request.Location.Name = "Changed payload"
		_, err = admin.CreateLocation(ctx, connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("changed retry: %v", err)
		}
	})
	t.Run("retry Save fault rolls back Location and Category writes", func(t *testing.T) {
		request := newCreate("Rollback proof")
		// The retry record is saved after the Location and Category links. Verify
		// those writes are visible inside the transaction before raising the fault.
		_, err := pool.Exec(ctx, `CREATE SEQUENCE admin_fault_observed; CREATE FUNCTION fail_admin_retry_save() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.method='CreateLocation' THEN IF NOT EXISTS (SELECT 1 FROM locations WHERE id=NEW.resource_id) OR (SELECT count(*) FROM location_categories WHERE location_id=NEW.resource_id) <> 2 THEN RAISE EXCEPTION 'fault did not follow resource writes'; END IF; PERFORM nextval('admin_fault_observed'); RAISE EXCEPTION 'injected retry save failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_admin_retry_save BEFORE INSERT ON supplier_idempotency FOR EACH ROW EXECUTE FUNCTION fail_admin_retry_save()`)
		if err != nil {
			t.Fatal(err)
		}
		cleanup := func() {
			if _, err := pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS fail_admin_retry_save ON supplier_idempotency; DROP FUNCTION IF EXISTS fail_admin_retry_save(); DROP SEQUENCE IF EXISTS admin_fault_observed`); err != nil {
				t.Error(err)
			}
		}
		defer cleanup()
		var beforeLinks int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM location_categories`).Scan(&beforeLinks); err != nil {
			t.Fatal(err)
		}
		_, err = admin.CreateLocation(ctx, connect.NewRequest(request))
		if connect.CodeOf(err) != connect.CodeInternal {
			t.Fatalf("fault result: %v", err)
		}
		// Sequence increments survive transaction rollback, unlike table writes.
		var observed bool
		if err := pool.QueryRow(ctx, `SELECT is_called FROM admin_fault_observed`).Scan(&observed); err != nil || !observed {
			t.Fatalf("fault did not observe completed resource and Category writes: %v %v", observed, err)
		}
		var locations, records, links int
		if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM locations WHERE name=$1), (SELECT count(*) FROM supplier_idempotency WHERE key=$2), (SELECT count(*) FROM location_categories)`, request.Location.Name, request.IdempotencyKey).Scan(&locations, &records, &links); err != nil {
			t.Fatal(err)
		}
		if locations != 0 || records != 0 || links != beforeLinks {
			t.Fatalf("rollback rows: %d %d %d before=%d", locations, records, links, beforeLinks)
		}
		cleanup()
		response, err := admin.CreateLocation(ctx, connect.NewRequest(request))
		if err != nil || response.Msg.Location.Revision != 1 || len(response.Msg.Location.Categories) != 2 {
			t.Fatalf("same key after rollback: %v %v", response, err)
		}
	})
	t.Run("concurrent stale update and masked clear", func(t *testing.T) {
		request := newCreate("Update proof")
		request.Location.Floor = proto.String("3")
		request.Location.Contact = proto.String("555")
		request.Location.OpensAt = &timeofday.TimeOfDay{Hours: 22}
		request.Location.ClosesAt = &timeofday.TimeOfDay{Hours: 2}
		response, err := admin.CreateLocation(ctx, connect.NewRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		loc := response.Msg.Location
		barrier, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer barrier.Rollback(context.Background()) //nolint:errcheck
		var pid int
		if err := barrier.QueryRow(ctx, `SELECT pg_backend_pid() FROM locations WHERE id=$1 FOR UPDATE`, loc.Id).Scan(&pid); err != nil {
			t.Fatal(err)
		}
		winners := make(chan *pb.Location, 2)
		errors := make(chan error, 2)
		start := make(chan struct{})
		for _, floor := range []string{"B1", "B2"} {
			go func() {
				<-start
				response, err := admin.UpdateLocation(ctx, connect.NewRequest(&pb.UpdateLocationRequest{Id: loc.Id, ExpectedRevision: loc.Revision, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}}, Location: &pb.LocationInput{Floor: proto.String(floor)}}))
				if err == nil {
					winners <- response.Msg.Location
				}
				errors <- err
			}()
		}
		close(start)
		waitForDatabaseBlock(t, ctx, pool, pid, "%locations%FOR UPDATE%", 2)
		if err := barrier.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		success, stale := 0, 0
		for i := 0; i < 2; i++ {
			err := <-errors
			if err == nil {
				success++
			} else if connect.CodeOf(err) == connect.CodeAborted {
				stale++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || stale != 1 {
			t.Fatalf("updates success=%d stale=%d", success, stale)
		}
		winner := <-winners
		persisted, err := discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: loc.Id}))
		if err != nil || !proto.Equal(winner, persisted.Msg.Location) || (winner.GetFloor() != "B1" && winner.GetFloor() != "B2") {
			t.Fatalf("persisted winner: %v %v", persisted, err)
		}
		cleared, err := admin.UpdateLocation(ctx, connect.NewRequest(&pb.UpdateLocationRequest{Id: loc.Id, ExpectedRevision: 2, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor", "contact", "details", "opens_at", "closes_at"}}, Location: &pb.LocationInput{Name: "", BuildingId: "invalid unmasked"}}))
		if err != nil {
			t.Fatal(err)
		}
		got := cleared.Msg.Location
		if got.Revision != 3 || got.Floor != nil || got.Contact != nil || got.Details != "" || got.OpensAt != nil || got.ClosesAt != nil || got.Name != loc.Name || len(got.Categories) != 2 {
			t.Fatalf("clear: %v", got)
		}
	})
	t.Run("archive preserves history repeats and historical replay", func(t *testing.T) {
		request, loc := create(t, "Archive proof")
		activeRequest := &pb.CreateDisablementRequest{LocationId: loc.Id, Reason: "active proof", IdempotencyKey: uuid.NewString()}
		active, err := disablement.CreateDisablement(ctx, connect.NewRequest(activeRequest))
		if err != nil {
			t.Fatal(err)
		}
		scheduled, err := disablement.CreateDisablement(ctx, connect.NewRequest(&pb.CreateDisablementRequest{LocationId: loc.Id, Reason: "scheduled proof", IdempotencyKey: uuid.NewString(), StartsAt: timestamppb.New(time.Now().Add(time.Hour))}))
		// Indefinite active intervals overlap any later interval. End the active
		// interval naturally in the future before scheduling an adjacent interval.
		if connect.CodeOf(err) != connect.CodeAlreadyExists {
			t.Fatalf("expected overlap: %v %v", scheduled, err)
		}
		if _, err := pool.Exec(ctx, `UPDATE location_disablements SET ends_at=now()+interval '30 minutes' WHERE id=$1`, active.Msg.Disablement.Id); err != nil {
			t.Fatal(err)
		}
		scheduled, err = disablement.CreateDisablement(ctx, connect.NewRequest(&pb.CreateDisablementRequest{LocationId: loc.Id, Reason: "scheduled proof", IdempotencyKey: uuid.NewString(), StartsAt: timestamppb.New(time.Now().Add(time.Hour))}))
		if err != nil {
			t.Fatal(err)
		}
		history, err := disablement.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: loc.Id}))
		if err != nil {
			t.Fatal(err)
		}
		archived, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: loc.Id}))
		if err != nil {
			t.Fatal(err)
		}
		again, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: loc.Id}))
		if err != nil || !proto.Equal(archived.Msg.Location, again.Msg.Location) {
			t.Fatalf("archive repeat: %v %v", again, err)
		}
		archivedHistory, err := disablement.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: loc.Id}))
		if err != nil || !proto.Equal(history.Msg, archivedHistory.Msg) {
			t.Fatalf("archive rewrote history: %v %v", archivedHistory, err)
		}
		retry, err := admin.CreateLocation(ctx, connect.NewRequest(request))
		if err != nil || !proto.Equal(retry.Msg.Location, archived.Msg.Location) {
			t.Fatalf("archived Create replay: %v %v", retry, err)
		}
		var expectedReplay *pb.Disablement
		for _, record := range history.Msg.Disablements {
			if record.Id == active.Msg.Disablement.Id {
				expectedReplay = record
			}
		}
		historical, err := disablement.CreateDisablement(ctx, connect.NewRequest(activeRequest))
		if err != nil || expectedReplay == nil || !proto.Equal(historical.Msg.Disablement, expectedReplay) {
			t.Fatalf("archived disablement replay snapshot: %v %v", historical, err)
		}
		_, err = disablement.CreateDisablement(ctx, connect.NewRequest(&pb.CreateDisablementRequest{LocationId: loc.Id, Reason: "new rejected", IdempotencyKey: uuid.NewString()}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatal(err)
		}
		direct, err := discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: loc.Id}))
		if err != nil || direct.Msg.Location.CurrentDisablement.GetId() != active.Msg.Disablement.Id {
			t.Fatalf("archived current warning: %v %v", direct, err)
		}
		list, err := discovery.ListLocations(ctx, connect.NewRequest(&pb.ListLocationsRequest{Search: "Archive proof"}))
		if err != nil || list.Msg.TotalItems != 0 {
			t.Fatalf("archive visibility: %v %v", list, err)
		}
		restored, err := admin.UnarchiveLocation(ctx, connect.NewRequest(&pb.UnarchiveLocationRequest{Id: loc.Id}))
		if err != nil {
			t.Fatal(err)
		}
		restoredAgain, err := admin.UnarchiveLocation(ctx, connect.NewRequest(&pb.UnarchiveLocationRequest{Id: loc.Id}))
		if err != nil || !proto.Equal(restored.Msg.Location, restoredAgain.Msg.Location) {
			t.Fatalf("restore repeat: %v %v", restoredAgain, err)
		}
		if restored.Msg.Location.ArchivedAt != nil || restored.Msg.Location.CurrentDisablement.GetId() != active.Msg.Disablement.Id || restored.Msg.Location.Revision != loc.Revision+2 || !proto.Equal(loc.CreatedAt, restored.Msg.Location.CreatedAt) {
			t.Fatalf("restore preserved state: %v", restored)
		}
		visible, err := discovery.ListLocations(ctx, connect.NewRequest(&pb.ListLocationsRequest{Search: "Archive proof"}))
		if err != nil || visible.Msg.TotalItems != 1 || visible.Msg.Locations[0].Id != loc.Id {
			t.Fatalf("restored visibility: %v %v", visible, err)
		}
		if _, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: loc.Id})); err != nil {
			t.Fatal(err)
		}
		_, err = disablement.CancelDisablement(ctx, connect.NewRequest(&pb.CancelDisablementRequest{Id: active.Msg.Disablement.Id}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("cancel active: %v", err)
		}
		_, err = disablement.EndDisablement(ctx, connect.NewRequest(&pb.EndDisablementRequest{Id: scheduled.Msg.Disablement.Id}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("end scheduled: %v", err)
		}
		unchanged, err := disablement.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: loc.Id}))
		if err != nil || !proto.Equal(history.Msg, unchanged.Msg) {
			t.Fatalf("invalid transitions rewrote history: %v %v", unchanged, err)
		}
		ended, err := disablement.EndDisablement(ctx, connect.NewRequest(&pb.EndDisablementRequest{Id: active.Msg.Disablement.Id}))
		if err != nil || ended.Msg.Disablement.State != pb.DisablementState_DISABLEMENT_STATE_ENDED {
			t.Fatalf("end archived: %v %v", ended, err)
		}
		cancelled, err := disablement.CancelDisablement(ctx, connect.NewRequest(&pb.CancelDisablementRequest{Id: scheduled.Msg.Disablement.Id}))
		if err != nil || cancelled.Msg.Disablement.State != pb.DisablementState_DISABLEMENT_STATE_CANCELLED {
			t.Fatalf("cancel archived: %v %v", cancelled, err)
		}
		restored, err = admin.UnarchiveLocation(ctx, connect.NewRequest(&pb.UnarchiveLocationRequest{Id: loc.Id}))
		if err != nil || restored.Msg.Location.CurrentDisablement != nil {
			t.Fatalf("terminal history not reactivated: %v %v", restored, err)
		}
		terminalHistory, err := disablement.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: loc.Id}))
		if err != nil || len(terminalHistory.Msg.Disablements) != 2 {
			t.Fatalf("lost terminal history: %v %v", terminalHistory, err)
		}
		for _, d := range terminalHistory.Msg.Disablements {
			if d.Id == active.Msg.Disablement.Id && !proto.Equal(d, ended.Msg.Disablement) || d.Id == scheduled.Msg.Disablement.Id && !proto.Equal(d, cancelled.Msg.Disablement) {
				t.Fatalf("rewrote terminal history: %v", d)
			}
		}
		visible, err = discovery.ListLocations(ctx, connect.NewRequest(&pb.ListLocationsRequest{Search: "Archive proof"}))
		if err != nil || visible.Msg.TotalItems != 1 || visible.Msg.Locations[0].CurrentDisablement != nil {
			t.Fatalf("ended list warning: %v %v", visible, err)
		}
	})
	t.Run("restore does not reactivate naturally expired intervals", func(t *testing.T) {
		_, loc := create(t, "Natural expiry proof")
		d, err := disablement.CreateDisablement(ctx, connect.NewRequest(&pb.CreateDisablementRequest{
			LocationId: loc.Id, Reason: "finite warning", IdempotencyKey: uuid.NewString(),
			StartsAt: timestamppb.New(time.Now().Add(-2 * time.Hour)), EndsAt: timestamppb.New(time.Now().Add(time.Hour)),
		}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: loc.Id})); err != nil {
			t.Fatal(err)
		}
		// Stage elapsed database time without sleeping or changing production clocks.
		if _, err := pool.Exec(ctx, `UPDATE location_disablements SET ends_at=now()-interval '1 hour' WHERE id=$1`, d.Msg.Disablement.Id); err != nil {
			t.Fatal(err)
		}
		restored, err := admin.UnarchiveLocation(ctx, connect.NewRequest(&pb.UnarchiveLocationRequest{Id: loc.Id}))
		if err != nil || restored.Msg.Location.ArchivedAt != nil || restored.Msg.Location.CurrentDisablement != nil {
			t.Fatalf("naturally expired restore: %v %v", restored, err)
		}
		history, err := disablement.ListDisablements(ctx, connect.NewRequest(&pb.ListDisablementsRequest{LocationId: loc.Id}))
		if err != nil || len(history.Msg.Disablements) != 1 || history.Msg.Disablements[0].State != pb.DisablementState_DISABLEMENT_STATE_ENDED || history.Msg.Disablements[0].EndedAt != nil {
			t.Fatalf("natural terminal history: %v %v", history, err)
		}
	})
	t.Run("archive and disablement share the Location row lock", func(t *testing.T) {
		for _, archiveFirst := range []bool{true, false} {
			_, loc := create(t, "Lock proof "+uuid.NewString())
			tx, err := pool.Begin(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback(context.Background()) //nolint:errcheck
			var pid int
			if err := tx.QueryRow(ctx, `SELECT pg_backend_pid() FROM locations WHERE id=$1 FOR UPDATE`, loc.Id).Scan(&pid); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			archiveResults := make(chan *pb.Location, 1)
			if archiveFirst {
				if _, err := tx.Exec(ctx, `UPDATE locations SET archived_at=now(),revision=revision+1 WHERE id=$1`, loc.Id); err != nil {
					t.Fatal(err)
				}
				go func() {
					_, err := disablement.CreateDisablement(ctx, connect.NewRequest(&pb.CreateDisablementRequest{LocationId: loc.Id, Reason: "blocked creation", IdempotencyKey: uuid.NewString()}))
					done <- err
				}()
			} else {
				if _, err := tx.Exec(ctx, `INSERT INTO location_disablements(location_id,starts_at,reason,created_by) VALUES($1,now(),'committing warning','admin')`, loc.Id); err != nil {
					t.Fatal(err)
				}
				go func() {
					response, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: loc.Id}))
					if err == nil {
						archiveResults <- response.Msg.Location
					}
					done <- err
				}()
			}
			waitForDatabaseBlock(t, ctx, pool, pid, "%locations%FOR UPDATE%", 1)
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if archiveFirst && connect.CodeOf(err) != connect.CodeFailedPrecondition || !archiveFirst && err != nil {
				t.Fatalf("archiveFirst=%v result=%v", archiveFirst, err)
			}
			direct, err := discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: loc.Id}))
			if err != nil || direct.Msg.Location.ArchivedAt == nil {
				t.Fatalf("committed archive: %v %v", direct, err)
			}
			if !archiveFirst {
				response := <-archiveResults
				if !proto.Equal(response, direct.Msg.Location) {
					t.Fatalf("archive response used stale state: %v", response)
				}
			}
			if archiveFirst && direct.Msg.Location.CurrentDisablement != nil || !archiveFirst && direct.Msg.Location.CurrentDisablement == nil {
				t.Fatalf("serialized warning: %v", direct)
			}
		}
	})
	t.Run("classification replacement masked clocks and archived ALL view", func(t *testing.T) {
		_, supplier := create(t, "Classification proof")
		ordinary := proto.Clone(input).(*pb.LocationInput)
		ordinary.Name = supplier.Name
		ordinary.IsSupplier = proto.Bool(false)
		ordinary.CategoryIds = nil
		created, err := admin.CreateLocation(ctx, connect.NewRequest(&pb.CreateLocationRequest{Location: ordinary, IdempotencyKey: uuid.NewString()}))
		if err != nil || created.Msg.Location.IsSupplier || len(created.Msg.Location.Categories) != 0 || created.Msg.Location.Id == supplier.Id {
			t.Fatalf("duplicate-name ordinary create=%v err=%v", created, err)
		}
		id := created.Msg.Location.Id
		classified, err := admin.UpdateLocation(ctx, connect.NewRequest(&pb.UpdateLocationRequest{
			Id: id, ExpectedRevision: 1, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_supplier", "category_ids"}},
			Location: &pb.LocationInput{IsSupplier: proto.Bool(true), CategoryIds: input.CategoryIds},
		}))
		if err != nil || !classified.Msg.Location.IsSupplier || len(classified.Msg.Location.Categories) != 1 || classified.Msg.Location.Revision != 2 {
			t.Fatalf("atomic classification=%v err=%v", classified, err)
		}
		ignored, err := admin.UpdateLocation(ctx, connect.NewRequest(&pb.UpdateLocationRequest{
			Id: id, ExpectedRevision: 2, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"floor"}},
			Location: &pb.LocationInput{Floor: proto.String("B2"), OpensAt: &timeofday.TimeOfDay{Hours: 8, Seconds: 3}},
		}))
		if err != nil || ignored.Msg.Location.Revision != 3 || ignored.Msg.Location.GetFloor() != "B2" || ignored.Msg.Location.OpensAt != nil {
			t.Fatalf("unmasked malformed clock=%v err=%v", ignored, err)
		}
		read, err := discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: id}))
		if err != nil || !proto.Equal(ignored.Msg.Location, read.Msg.Location) {
			t.Fatalf("persisted masked update=%v err=%v", read, err)
		}
		replaced, err := admin.UpdateLocation(ctx, connect.NewRequest(&pb.UpdateLocationRequest{
			Id: id, ExpectedRevision: 3, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name", "category_ids"}},
			Location: &pb.LocationInput{Name: supplier.Name, CategoryIds: []string{"29a7cb1e-44f2-4c68-825f-1ce39b78e44a"}},
		}))
		if err != nil || replaced.Msg.Location.Revision != 4 || len(replaced.Msg.Location.Categories) != 1 || replaced.Msg.Location.Categories[0].Id != "29a7cb1e-44f2-4c68-825f-1ce39b78e44a" {
			t.Fatalf("Category replacement=%v err=%v", replaced, err)
		}
		read, err = discovery.GetLocation(ctx, connect.NewRequest(&pb.GetLocationRequest{Id: id}))
		if err != nil || !proto.Equal(replaced.Msg.Location, read.Msg.Location) {
			t.Fatalf("persisted replacement=%v err=%v", read, err)
		}
		if _, err := admin.ArchiveLocation(ctx, connect.NewRequest(&pb.ArchiveLocationRequest{Id: supplier.Id})); err != nil {
			t.Fatal(err)
		}
		active, err := discovery.ListLocations(ctx, connect.NewRequest(&pb.ListLocationsRequest{Search: supplier.Name}))
		if err != nil || active.Msg.TotalItems != 1 || active.Msg.Locations[0].Id != id {
			t.Fatalf("active classification view=%v err=%v", active, err)
		}
		all, err := discovery.ListLocations(ctx, connect.NewRequest(&pb.ListLocationsRequest{Search: supplier.Name, StatusView: pb.LocationStatusView_LOCATION_STATUS_VIEW_ALL}))
		if err != nil || all.Msg.TotalItems != 2 {
			t.Fatalf("archived ALL view=%v err=%v", all, err)
		}
	})

}

func waitForDatabaseBlock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, pid int, query string, want int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var blocked int
		err := pool.QueryRow(ctx, `WITH RECURSIVE blocked AS (
			SELECT pid FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid))
			UNION
			SELECT activity.pid FROM pg_stat_activity activity JOIN blocked
				ON blocked.pid=ANY(pg_blocking_pids(activity.pid))
		) SELECT count(*) FROM pg_stat_activity
			WHERE pid IN (SELECT pid FROM blocked) AND query LIKE $2`, pid, query).Scan(&blocked)
		if err != nil {
			t.Fatalf("observe database lock wait for %q (%d clients): %v", query, want, err)
		}
		if blocked >= want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("observe database lock wait for %q (%d clients): %v", query, want, ctx.Err())
		case <-ticker.C:
		}
	}
}
