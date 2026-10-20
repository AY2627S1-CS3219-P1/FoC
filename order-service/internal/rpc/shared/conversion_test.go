package shared

import (
	"context"
	"testing"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

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
