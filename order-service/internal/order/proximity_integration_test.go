//go:build integration

package order

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresProximityPages(t *testing.T) {
	f := setup(t)
	service := NewService(f.repo)
	caller := Caller{ID: uuid.NewString()}
	now := time.Now().UTC().Truncate(time.Microsecond)
	ids := []string{
		"10000000-0000-4000-8000-000000000001",
		"10000000-0000-4000-8000-000000000002",
		"10000000-0000-4000-8000-000000000003",
		"10000000-0000-4000-8000-000000000004",
		"10000000-0000-4000-8000-000000000005",
	}
	pickup := uuid.NewString()
	points := []*Coordinates{{Latitude: 60}, {}, {}, {Latitude: 1}, nil}
	for i, id := range ids {
		o := sampleOrder()
		o.ID, o.Status, o.Pickup.LocationID = id, AwaitingCourier, pickup
		o.Pickup.Coordinates = points[i]
		o.CreatedAt, o.UpdatedAt = now.Add(-time.Duration(i)*time.Hour), now
		// Equal distance and time must fall back to ID DESC.
		if i == 2 {
			o.CreatedAt = now.Add(-time.Hour)
		}
		f.seed(t, o)
	}
	hidden := sampleOrder()
	hidden.Status, hidden.RequesterID = AwaitingCourier, caller.ID
	hidden.Pickup.Coordinates = &Coordinates{}
	f.seed(t, hidden)

	var got []string
	for page := int32(1); page <= 3; page++ {
		result, err := service.ListOpenOrders(f.ctx, caller, ListRequest{
			Sort: SortProximity, Origin: &Coordinates{}, PickupID: pickup,
			DeliveryBuildingName: "PGP", Page: Page{Number: page, Size: 2},
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.PageInfo.Total != 5 || result.PageInfo.TotalPages() != 3 {
			t.Fatalf("counts %+v", result.PageInfo)
		}
		for _, o := range result.Orders {
			got = append(got, o.ID)
		}
	}
	want := []string{ids[2], ids[1], ids[3], ids[0], ids[4]}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	result, err := service.ListOpenOrders(f.ctx, caller, ListRequest{
		Sort: SortProximity, Origin: &Coordinates{}, DeliveryBuildingName: "no matches",
	})
	if err != nil || result.PageInfo.Total != 0 {
		t.Fatalf("filtered: %+v %v", result, err)
	}
	newest, err := service.ListOpenOrders(f.ctx, caller, ListRequest{PickupID: pickup})
	if err != nil || newest.Orders[0].ID != ids[0] {
		t.Fatalf("newest default: %+v %v", newest, err)
	}

	// Snapshot roundtrip retains coordinates even when another caller mutates a projection.
	read, err := f.repo.Get(f.ctx, ids[1])
	if err != nil || read.Pickup.Coordinates == nil || *read.Pickup.Coordinates != (Coordinates{}) {
		t.Fatalf("coordinate persistence: %+v %v", read.Pickup, err)
	}
}
