package outbox

import (
	"encoding/json"
	"testing"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"google.golang.org/protobuf/proto"
)

// OrderStatusChanged.previous_courier_id identifies the removed participant for
// withdrawal and admin unassignment. The current courier is absent in both cases.
func TestRemovedCourierSurvivesStatusEventWireEncoding(t *testing.T) {
	for _, action := range []app.Action{app.Withdraw, app.Unassign} {
		t.Run(string(action), func(t *testing.T) {
			courier := "previous-courier"
			at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
			payload, err := json.Marshal(app.FactsPayload{
				OrderID: "order", Revision: 8, RequesterID: "requester", PreviousCourierID: &courier,
				PreviousStatus: app.AwaitingPickup, Status: app.AwaitingCourier, Action: action, At: at,
			})
			if err != nil {
				t.Fatal(err)
			}
			actorKind := "user"
			if action == app.Unassign {
				actorKind = "admin"
			}
			actorID := "actor"
			message, err := encode(Fact{
				ID: "stable-event", OrderID: "order", Revision: 8, Version: 1,
				Kind: string(app.StatusChanged), ActorKind: actorKind, ActorID: &actorID, Payload: payload,
			})
			if err != nil {
				t.Fatal(err)
			}
			wire, err := proto.Marshal(message)
			if err != nil {
				t.Fatal(err)
			}
			var decoded events.OrderStatusChanged
			if err := proto.Unmarshal(wire, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.PreviousCourierId == nil || *decoded.PreviousCourierId != courier || decoded.CourierId != nil || decoded.EventId != "stable-event" {
				t.Fatalf("removed courier was lost or reassigned: %v", &decoded)
			}
		})
	}
}
