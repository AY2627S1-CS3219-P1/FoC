package order

import (
	"testing"
)

// NFR-06.2 and the approved COM3/Room 02-15 scenario define these expectations.
// Former-courier history must not confer current participant permissions.
func TestDeliveryProjectionSeparatesHistoryFromSensitiveAccess(t *testing.T) {
	courier, instructions, floor, disposition, reason := "current-courier", "Call at Room 02-15", "2", "Room 02-15", "Left at Room 02-15"
	original := Order{
		RequesterID:          "requester",
		Pickup:               LocationSnapshot{LocationID: "pickup-location", Name: "Pickup store", BuildingName: "COM3", Floor: &floor},
		CourierID:            &courier,
		Delivery:             LocationSnapshot{LocationID: "exact-location", Name: "Room 02-15", BuildingName: "COM3", Floor: &floor},
		DeliveryInstructions: &instructions,
		ItemLocation:         &disposition,
		CloseReason:          &reason,
	}
	for _, caller := range []Caller{
		{ID: "browser"}, {ID: "former-courier", Suspended: true}, {ID: "admin", Admin: true},
	} {
		view := ProjectForCaller(caller, original)
		// The browse contract permits an exact pickup filter; destination data
		// is the restricted location under the accepted privacy decision.
		if view.Pickup.LocationID != "pickup-location" || view.Pickup.Name != "Pickup store" || view.Pickup.Floor == nil || *view.Pickup.Floor != floor {
			t.Fatalf("caller %+v lost the browseable pickup snapshot: %+v", caller, view.Pickup)
		}
		if view.Delivery.BuildingName != "COM3" || view.Delivery.LocationID != "" || view.Delivery.Name != "" || view.Delivery.Floor != nil ||
			view.DeliveryInstructions != nil || view.ItemLocation != nil || view.CloseReason != nil {
			t.Fatalf("caller %+v received sensitive projection: %+v", caller, view)
		}
	}
	for _, caller := range []Caller{{ID: "requester", Suspended: true}, {ID: courier}} {
		view := ProjectForCaller(caller, original)
		if view.Delivery.LocationID != "exact-location" || view.DeliveryInstructions == nil || view.ItemLocation == nil {
			t.Fatalf("current participant lost delivery access: %+v", view)
		}
	}
	if original.Delivery.LocationID != "exact-location" || original.DeliveryInstructions == nil || original.CloseReason == nil {
		t.Fatal("response projection modified the aggregate snapshot")
	}
	view := ProjectForCaller(Caller{ID: courier}, original)
	*view.Delivery.Floor = "changed"
	if *original.Delivery.Floor != "2" {
		t.Fatal("projected response aliases aggregate metadata")
	}
}
