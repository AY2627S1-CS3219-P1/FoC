package order

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestNewChangeCreatesInitialPendingFactAndAudit(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	after := Order{ID: "o1", RequesterID: "u1", Status: PendingReservation, ReservedCredits: 500, Revision: 1, UpdatedAt: at}
	change, err := NewChange(nil, after, Actor{Kind: User, ID: "u1"}, Create, nil, testIDs())
	if err != nil {
		t.Fatal(err)
	}
	if change.ExpectedRevision != 0 || change.Audit.Status != PendingReservation || change.Audit.PreviousStatus != "" || string(change.Audit.Details) != `{}` {
		t.Fatalf("creation audit/change = %+v", change)
	}
	if len(change.Facts) != 1 || change.Facts[0].Kind != StatusChanged || change.Facts[0].Version != 1 {
		t.Fatalf("creation facts = %+v", change.Facts)
	}
	var payload FactsPayload
	if err := json.Unmarshal(change.Facts[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.OrderID != "o1" || payload.Revision != 1 || payload.RequesterID != "u1" || payload.ReservedCredits != 500 || payload.Status != PendingReservation || payload.Action != Create {
		t.Fatalf("creation payload = %+v", payload)
	}
}

func TestNewChangeStatusFactsAndOutcomes(t *testing.T) {
	cases := []struct {
		name        string
		before      Order
		afterStatus Status
		actor       Actor
		action      Action
	}{
		{name: "confirmation", before: Order{Status: PendingReservation}, afterStatus: AwaitingCourier, actor: Actor{Kind: System}, action: ConfirmReservation},
		{name: "completion", before: Order{Status: Delivered, CourierID: strptr("courier")}, afterStatus: Completed, actor: Actor{Kind: User, ID: "requester"}, action: ConfirmReceipt},
		{name: "cancellation", before: Order{Status: AwaitingCourier}, afterStatus: Cancelled, actor: Actor{Kind: User, ID: "requester"}, action: Cancel},
		{name: "expiry", before: Order{Status: AwaitingCourier}, afterStatus: Expired, actor: Actor{Kind: System}, action: Expire},
		{name: "failure", before: Order{Status: Delivering, CourierID: strptr("courier")}, afterStatus: Failed, actor: Actor{Kind: System}, action: Timeout},
		{name: "rejection", before: Order{Status: PendingReservation}, afterStatus: Rejected, actor: Actor{Kind: System}, action: RefuseReservation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := tc.before
			before.ID, before.RequesterID, before.Revision = "o1", "requester", 4
			before.ReservedCredits = 200
			before.DeliveryDuration = 12 * time.Minute
			after := before
			after.Status = tc.afterStatus
			after.Revision++
			at := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
			after.UpdatedAt = at
			change, err := NewChange(&before, after, tc.actor, tc.action, nil, testIDs())
			if err != nil {
				t.Fatal(err)
			}
			wantFacts := 1
			if tc.afterStatus == Completed || tc.afterStatus == Cancelled || tc.afterStatus == Expired || tc.afterStatus == Failed {
				wantFacts = 2
			}
			if len(change.Facts) != wantFacts || change.Facts[0].Kind != StatusChanged {
				t.Fatalf("facts for %s = %+v", tc.afterStatus, change.Facts)
			}
			if wantFacts == 2 && change.Facts[1].Kind != Outcome {
				t.Fatalf("outcome for %s = %+v", tc.afterStatus, change.Facts)
			}
			if tc.afterStatus == Rejected && len(change.Facts) != 1 {
				t.Fatalf("rejection must not create a settlement outcome: %+v", change.Facts)
			}
			var payload FactsPayload
			if err := json.Unmarshal(change.Facts[0].Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.RequesterID != "requester" || !reflect.DeepEqual(payload.CourierID, after.CourierID) || payload.ReservedCredits != 200 || payload.DeliveryDurationNS != int64(12*time.Minute) || payload.At != at {
				t.Fatalf("fact omitted settlement context: %+v", payload)
			}
		})
	}
}

func TestNewChangeCourierCancellationAfterPickupRequestsWarning(t *testing.T) {
	picked := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	courier := "courier"
	before := Order{ID: "o1", RequesterID: "requester", CourierID: &courier, Status: Delivering, Revision: 3, PickedUpAt: &picked}
	after := before
	after.Status = Failed
	after.Revision++
	after.UpdatedAt = picked.Add(time.Minute)
	change, err := NewChange(&before, after, Actor{Kind: User, ID: courier}, Cancel, nil, testIDs())
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Facts) != 3 || change.Facts[0].Kind != StatusChanged || change.Facts[1].Kind != Outcome || change.Facts[2].Kind != CourierWarning {
		t.Fatalf("courier cancellation facts = %+v", change.Facts)
	}
	var payload FactsPayload
	if err := json.Unmarshal(change.Facts[2].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.AfterPickup || payload.PickedUpAt == nil || !payload.PickedUpAt.Equal(picked) {
		t.Fatalf("warning missing pickup context: %+v", payload)
	}
}

func TestNewChangeMetadataAuditHasNoStatusFact(t *testing.T) {
	picked := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	before := Order{ID: "o1", RequesterID: "u1", CourierID: strptr("courier"), Status: Cancelled, Revision: 8, PickedUpAt: &picked}
	after := before
	after.Revision++
	after.ItemLocation = strptr("front desk")
	change, err := NewChange(&before, after, Actor{Kind: User, ID: "courier"}, RecordItemLocation, json.RawMessage(`{"location":"front desk"}`), testIDs())
	if err != nil {
		t.Fatal(err)
	}
	if change.ExpectedRevision != 8 || change.Audit.Status != Cancelled || change.Audit.PreviousStatus != Cancelled || len(change.Facts) != 0 {
		t.Fatalf("metadata change = %+v", change)
	}
}

func TestNewChangeRejectsBadRevisionIdentityAndDetails(t *testing.T) {
	picked := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	base := Order{ID: "o1", RequesterID: "u1", CourierID: strptr("courier"), Status: Cancelled, Revision: 1, PickedUpAt: &picked}
	validAfter := func(id string, revision int64) Order {
		return Order{ID: id, RequesterID: "u1", CourierID: strptr("courier"), Status: Cancelled, Revision: revision, PickedUpAt: &picked}
	}
	cases := []struct {
		name   string
		before *Order
		after  Order
		detail json.RawMessage
	}{
		{name: "revision did not advance", before: &base, after: base},
		{name: "identity changed", before: &base, after: validAfter("o2", 2)},
		{name: "invalid details", before: &base, after: validAfter("o1", 2), detail: json.RawMessage("{")},
		{name: "oversized details", before: &base, after: validAfter("o1", 2), detail: json.RawMessage(fmt.Sprintf(`{"payload":%q}`, string(make([]byte, 4100))))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewChange(tc.before, tc.after, Actor{Kind: User, ID: "courier"}, RecordItemLocation, tc.detail, testIDs()); !errors.Is(err, ErrInvalid) {
				t.Fatalf("NewChange error = %v, want ErrInvalid", err)
			}
		})
	}
}

func testIDs() func() string {
	n := 0
	return func() string { n++; return fmt.Sprintf("event-%d", n) }
}
