package outbox

import (
	"testing"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
)

// Agreed Q7/R1.4.2: timeout failure requests a zero-payable warning. The timer
// owner determines when Timeout is due; Credit still owns ledger settlement.
func TestTerminalWarningPolicyAndEncoding(t *testing.T) {
	picked := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	courier := uuid.NewString()
	for _, tc := range []struct {
		action app.Action
		actor  app.Actor
		reason events.CourierWarningReason
	}{
		{app.Cancel, app.Actor{Kind: app.User, ID: courier}, events.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP},
		{app.Timeout, app.Actor{Kind: app.System}, events.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			before := app.Order{ID: uuid.NewString(), RequesterID: uuid.NewString(), CourierID: &courier,
				Status: app.Delivering, Revision: 4, PickedUpAt: &picked, DeliveryDuration: 20 * time.Minute}
			after := before
			after.Status, after.Revision, after.UpdatedAt = app.Failed, 5, picked.Add(40*time.Minute)
			change, err := app.NewChange(&before, after, tc.actor, tc.action, nil, uuid.NewString)
			if err != nil {
				t.Fatal(err)
			}
			warnings := 0
			for _, fact := range change.Facts {
				if fact.Kind != app.CourierWarning {
					continue
				}
				warnings++
				message, err := encode(Fact{ID: fact.ID, OrderID: before.ID, Revision: 5, Kind: string(fact.Kind), Version: fact.Version, Payload: fact.Payload})
				if err != nil {
					t.Fatal(err)
				}
				warning := message.(*events.CourierWarningRequested)
				if warning.CourierId != courier || warning.Reason != tc.reason {
					t.Fatalf("wrong warning: %v", warning)
				}
			}
			if warnings != 1 {
				t.Fatalf("warning count=%d", warnings)
			}
		})
	}
}
