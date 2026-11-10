package order

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestProximityOriginValidation(t *testing.T) {
	caller := Caller{ID: "11111111-1111-4111-8111-111111111111"}
	service := NewService(nil)
	for _, point := range []*Coordinates{nil, {Latitude: 91}, {Longitude: -181}, {Latitude: math.NaN()}, {Longitude: math.Inf(1)}} {
		_, err := service.ListOpenOrders(context.Background(), caller, ListRequest{Sort: SortProximity, Origin: point})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("origin %+v: %v", point, err)
		}
	}
	for _, point := range []Coordinates{{}, {Latitude: -90, Longitude: 180}, {Latitude: 90, Longitude: -180}} {
		if !point.IsValid() {
			t.Fatalf("valid origin rejected: %+v", point)
		}
	}
}

func TestCoordinateProjectionIsIndependentAndPrivate(t *testing.T) {
	original := sampleCoordinateOrder()
	public := ProjectForCaller(Caller{ID: "viewer"}, original)
	if public.Delivery.Coordinates != nil {
		t.Fatal("delivery coordinates leaked")
	}
	if public.Pickup.Coordinates == nil {
		t.Fatal("pickup coordinates missing")
	}
	public.Pickup.Coordinates.Latitude = 25
	participant := ProjectForCaller(Caller{ID: original.RequesterID}, original)
	participant.Delivery.Coordinates.Longitude = 30
	if original.Pickup.Coordinates.Latitude != 1 || original.Delivery.Coordinates.Longitude != 104 {
		t.Fatal("projection changed original coordinates")
	}
}

func sampleCoordinateOrder() Order {
	return Order{RequesterID: "requester",
		Pickup:   LocationSnapshot{Coordinates: &Coordinates{Latitude: 1, Longitude: 103}},
		Delivery: LocationSnapshot{Coordinates: &Coordinates{Latitude: 2, Longitude: 104}}}
}
