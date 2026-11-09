//go:build integration

package order

import (
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/pressly/goose/v3"
	"os"
	"testing"
	"time"
)

// R1.8/U6.7 and the approved former-courier scenario require retained history,
// while NFR-05/06 forbid historical mutation authority or exact-location access.
func TestAssignmentHistoryPrivacyAndAtomicity(t *testing.T) {
	f := setup(t)
	s := NewService(f.repo)
	o := f.create(t)
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	first, second := Actor{Kind: User, ID: uuid.NewString()}, Actor{Kind: User, ID: uuid.NewString()}
	f.mustTransition(t, o.ID, Assign, first)
	f.mustTransition(t, o.ID, Withdraw, first)
	f.mustTransition(t, o.ID, Assign, first)
	f.mustTransition(t, o.ID, Unassign, Actor{Kind: Admin, ID: uuid.NewString()})
	f.mustTransition(t, o.ID, Assign, second)
	for _, a := range []Action{Pickup, Deliver} {
		f.mustTransition(t, o.ID, a, second)
	}
	f.mustTransition(t, o.ID, ConfirmReceipt, Actor{Kind: User, ID: o.RequesterID})
	for _, c := range []Caller{{ID: first.ID}, {ID: first.ID, Suspended: true}, {ID: uuid.NewString(), Admin: true}} {
		got, err := s.GetOrder(f.ctx, c, o.ID)
		if err != nil || got.Status != Completed || got.DeliveryInstructions != nil || got.Delivery.LocationID != "" ||
			got.Delivery.BuildingName != "PGP" {
			t.Fatalf("historical/admin safe view: %+v %v", got, err)
		}
	}
	for _, role := range []Role{AnyRole, CourierRole} {
		page, err := s.ListMyOrders(
			f.ctx,
			Caller{ID: first.ID, Suspended: true},
			ListRequest{Role: role, Status: Completed, Page: Page{Size: 1}},
		)
		if err != nil || page.PageInfo.Total != 1 || len(page.Orders) != 1 ||
			page.Orders[0].DeliveryInstructions != nil {
			t.Fatalf("duplicate or lost history: %+v %v", page, err)
		}
	}
	for _, c := range []Caller{{ID: o.RequesterID, Suspended: true}, {ID: second.ID}} {
		got, err := s.GetOrder(f.ctx, c, o.ID)
		if err != nil || got.Delivery.LocationID != o.Delivery.LocationID || got.DeliveryInstructions == nil {
			t.Fatalf("current exact view: %+v %v", got, err)
		}
	}
	if _, err := s.GetOrder(f.ctx, Caller{ID: uuid.NewString()}, o.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("stranger read", err)
	}
	raw, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || raw.HasParticipant(first.ID) || raw.Delivery.LocationID != o.Delivery.LocationID {
		t.Fatalf("history changed operational snapshot: %+v %v", raw, err)
	}
	var episodes, current int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*),count(*) FILTER(WHERE is_current) FROM order_assignment_history WHERE order_id=$1", o.ID).
		Scan(&episodes, &current); err != nil ||
		episodes != 3 ||
		current != 1 {
		t.Fatal(episodes, current, err)
	}

	// A failed history write must roll back Order/audit/outbox even if swallowed.
	next := f.create(t)
	f.mustTransition(t, next.ID, ConfirmReservation, Actor{Kind: System})
	if _, err := f.pool.Exec(
		f.ctx,
		`CREATE FUNCTION reject_episode() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'forced episode write failure'; END $$; CREATE TRIGGER reject_episode BEFORE INSERT ON order_assignment_history FOR EACH ROW EXECUTE FUNCTION reject_episode()`,
	); err != nil {
		t.Fatal(err)
	}
	err = f.repo.Within(f.ctx, func(tx Tx) error {
		before, e := tx.LockOrder(f.ctx, next.ID)
		if e != nil {
			return e
		}
		if _, e = tx.Relationships(f.ctx, next.ID); e != nil {
			return e
		}
		after := before
		after.Status = AwaitingPickup
		after.CourierID = &first.ID
		after.Revision++
		after.UpdatedAt = before.UpdatedAt.Add(time.Second)
		after.AssignedAt = &after.UpdatedAt
		change, e := NewChange(&before, after, first, Assign, nil, uuid.NewString)
		if e != nil {
			return e
		}
		_ = tx.Apply(f.ctx, change)
		return nil
	})
	if err == nil {
		t.Fatal("swallowed history failure committed")
	}
	raw, err = f.repo.Get(f.ctx, next.ID)
	audit, outbox, _ := f.counts(t, next.ID)
	if err != nil || raw.Revision != 2 || raw.CourierID != nil || raw.Status != AwaitingCourier || audit != 2 ||
		outbox != 2 {
		t.Fatalf("partial assignment commit: %+v audit=%d outbox=%d err=%v", raw, audit, outbox, err)
	}
}

// Coarse filtering must have identical list/count membership and never accept
// an exact Location UUID, even when two destinations share a building.
func TestCoarseDestinationFilter(t *testing.T) {
	f := setup(t)
	s := NewService(f.repo)
	caller := Caller{ID: uuid.NewString()}
	for i := range 3 {
		o := sampleOrder()
		o.Status = AwaitingCourier
		if i == 2 {
			o.Delivery.BuildingName = "COM3"
		}
		f.seed(t, o)
	}
	page, err := s.ListOpenOrders(f.ctx, caller, ListRequest{DeliveryBuildingName: "PGP", Page: Page{Size: 1}})
	if err != nil || len(page.Orders) != 1 || page.PageInfo.Total != 2 || page.Orders[0].Delivery.LocationID != "" {
		t.Fatal(page, err)
	}
	if _, err = s.ListOpenOrders(
		f.ctx,
		caller,
		ListRequest{DeliveryID: uuid.NewString()},
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal("exact filter accepted", err)
	}
	page, err = s.ListOpenOrders(f.ctx, caller, ListRequest{DeliveryBuildingName: "unknown"})
	if err != nil || len(page.Orders) != 0 || page.PageInfo.Total != 0 {
		t.Fatal(page, err)
	}
}

// Migration backfill uses durable facts/current identity, never accepted offers
// or invented assignment timestamps when only a removal identity survives.
func TestAssignmentHistoryMigrationEvidence(t *testing.T) {
	f := setup(t)
	o := f.create(t)
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	former, current := Actor{Kind: User, ID: uuid.NewString()}, Actor{Kind: User, ID: uuid.NewString()}
	f.mustTransition(t, o.ID, Assign, former)
	f.mustTransition(t, o.ID, Withdraw, former)
	f.mustTransition(t, o.ID, Assign, current)
	snapshot := sampleOrder()
	snapshot.Status = AwaitingPickup
	snapshot.CourierID = &current.ID
	f.seed(t, snapshot)
	removed := f.create(t)
	f.mustTransition(t, removed.ID, ConfirmReservation, Actor{Kind: System})
	f.mustTransition(t, removed.ID, Assign, former)
	f.mustTransition(t, removed.ID, Withdraw, former)
	if _, err := f.pool.Exec(
		f.ctx,
		"DELETE FROM order_outbox WHERE order_id=$1 AND payload->>'status'='awaiting_pickup'",
		removed.ID,
	); err != nil {
		t.Fatal(err)
	}
	offerOnly := uuid.NewString()
	if _, err := f.pool.Exec(
		f.ctx,
		"INSERT INTO offers VALUES($1,$2,$3,20,'accepted',now(),now())",
		uuid.NewString(),
		o.ID,
		offerOnly,
	); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", f.pool.Config().ConnString())
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.DownTo(f.ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(f.ctx); err != nil {
		t.Fatal(err)
	}
	service := NewService(f.repo)
	page, err := service.ListMyOrders(f.ctx, Caller{ID: former.ID, Suspended: true}, ListRequest{Role: CourierRole})
	if err != nil || page.PageInfo.Total != 2 || len(page.Orders) != 2 {
		t.Fatal(page, err)
	}
	page, err = service.ListMyOrders(f.ctx, Caller{ID: current.ID}, ListRequest{Role: CourierRole})
	if err != nil || page.PageInfo.Total != 2 {
		t.Fatal(page, err)
	}
	page, err = service.ListMyOrders(f.ctx, Caller{ID: offerOnly}, ListRequest{Role: CourierRole})
	if err != nil || page.PageInfo.Total != 0 {
		t.Fatal(page, err)
	}
	var unknown bool
	if err = f.pool.QueryRow(f.ctx, "SELECT assigned_at IS NULL AND assigned_revision IS NULL AND ended_revision IS NOT NULL AND source='removal_fact' FROM order_assignment_history WHERE order_id=$1", removed.ID).
		Scan(&unknown); err != nil ||
		!unknown {
		t.Fatal("fabricated assignment metadata", unknown, err)
	}
	// A migrated current snapshot can be removed through the same atomic hook.
	f.mustTransition(t, snapshot.ID, Unassign, Actor{Kind: Admin, ID: uuid.NewString()})
}
