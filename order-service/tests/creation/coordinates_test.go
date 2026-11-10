//go:build integration

package creation_test

import (
	"testing"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

func TestCreationCoordinateSnapshotsSurviveSupplierEdits(t *testing.T) {
	f := databaseFixture(t)
	s := newScenario(t, f)
	c := mount(t, f, s.creator)
	in := input(s.clock.Now())
	created, err := create(c, f.ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if created.Pickup.GetCoordinates().GetLongitude() != 103 || created.Pickup.Coordinates == nil ||
		created.Delivery.GetLocation().GetCoordinates().GetLongitude() != 103 {
		t.Fatalf("creation omitted coordinates: %v", created)
	}
	s.remotes.mu.Lock()
	s.remotes.locationLatitude = 25
	s.remotes.mu.Unlock()
	next, err := create(c, f.ctx, input(s.clock.Now()))
	if err != nil || next.Pickup.GetCoordinates().GetLatitude() != 25 {
		t.Fatalf("Supplier edit not observed: %v %v", next, err)
	}
	stored, err := f.repo.Get(f.ctx, created.Id)
	if err != nil || stored.Pickup.Coordinates == nil || stored.Pickup.Coordinates.Latitude != 0 ||
		stored.Delivery.Coordinates == nil || stored.Delivery.Coordinates.Latitude != 0 {
		t.Fatalf("Supplier edit changed snapshot: %+v %v", stored, err)
	}
	public, err := app.NewService(f.repo).
		GetOrder(f.ctx, app.Caller{ID: stored.RequesterID}, created.Id)
	public = app.ProjectForCaller(app.Caller{ID: "viewer"}, public)
	if err != nil || public.Delivery.Coordinates != nil || public.Pickup.Coordinates == nil {
		t.Fatalf("delivery redaction: %+v %v", public, err)
	}
}
