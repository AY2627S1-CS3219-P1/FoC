package shared

import (
	"context"
	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"testing"
	"time"
)

type imageRecorder struct{ calls int }

func (r *imageRecorder) ForOrder(context.Context, app.Caller, app.Order) ([]app.ItemImageView, error) {
	r.calls++
	return []app.ItemImageView{{ID: "image", DownloadURL: "https://fixture/item"}}, nil
}

// NFR-06.2 applies to shared mutation responses as well as Get/List. Historical
// membership must not cause a later attachment credential to be minted.
func TestOrderViewProtectsEveryResponse(t *testing.T) {
	courier, secret, room := "current", "Call Room 02-15", "Room 02-15"
	original := app.Order{RequesterID: "requester", CourierID: &courier, Status: app.Completed, Delivery: app.LocationSnapshot{LocationID: "exact", Name: room, BuildingName: "COM3"}, DeliveryInstructions: &secret, ItemLocation: &room, CloseReason: &room, ExpiresAt: time.Now().Add(time.Hour)}
	for _, c := range []app.Caller{{ID: "admin", Admin: true}, {ID: "former"}, {ID: "former", Suspended: true}} {
		recorder := &imageRecorder{}
		got, err := OrderView(context.Background(), c, original, recorder)
		if err != nil || got.Delivery != nil || got.DeliveryBuildingName != "COM3" || got.ItemLocation != nil || got.CloseReason != nil || len(got.ItemImages) != 0 || recorder.calls != 0 {
			t.Fatalf("unauthorized response: %+v calls=%d err=%v", got, recorder.calls, err)
		}
	}
	recorder := &imageRecorder{}
	got, err := OrderView(context.Background(), app.Caller{ID: "requester", Suspended: true}, original, recorder)
	if err != nil || got.Delivery.GetLocation().GetLocationId() != "exact" || got.Delivery.GetInstructions() != secret || len(got.ItemImages) != 1 || recorder.calls != 1 {
		t.Fatal(got, err)
	}
	if original.Delivery.LocationID != "exact" || original.ItemLocation == nil {
		t.Fatal("projection changed aggregate")
	}
}

func TestOrderViewsKeepsPageWhenSelectedOrderExpires(t *testing.T) {
	selectedAt := time.Now().Add(-time.Minute)
	orders := []app.Order{
		{ID: "just-expired", RequesterID: "requester", Status: app.AwaitingCourier, ExpiresAt: selectedAt.Add(time.Second)},
		{ID: "still-open", RequesterID: "requester", Status: app.AwaitingCourier, ExpiresAt: selectedAt.Add(time.Hour)},
	}
	// The read layer already selected both orders at selectedAt. Projection must
	// not recheck expiry or issue private image grants to a browsing courier.
	views, err := OrderViews(context.Background(), app.Caller{ID: "browser"}, orders, app.NewItemImages(nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 2 || views[0].Id != orders[0].ID || views[1].Id != orders[1].ID {
		t.Fatalf("lost selected orders: %v", views)
	}
	for _, view := range views {
		if len(view.ItemImages) != 0 {
			t.Fatal("browsing courier received private images")
		}
	}
}
