package notify

import (
	"testing"

	"github.com/google/uuid"

	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
)

func TestRecipientsExcludeTheActor(t *testing.T) {
	admin := &eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_ADMIN, UserId: new(requesterID.String())}
	cases := []struct {
		name  string
		actor *eventsv1.Actor
		want  []role
	}{
		{"requester acts", user(requesterID), []role{courier}},
		{"courier acts", user(courierID), []role{requester}},
		{"system acts", system, []role{requester, courier}},
		// An admin isn't a participant, even with a matching ID.
		{"admin acts", admin, []role{requester, courier}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := recipients(event(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, eventsv1.OrderStatus_ORDER_STATUS_CANCELLED, tc.actor))
			if len(got) != len(tc.want) {
				t.Fatalf("recipients = %+v", got)
			}
			for i := range got {
				if got[i].role != tc.want[i] {
					t.Fatalf("recipients = %+v", got)
				}
			}
		})
	}
}

func TestUnassignedCourierIsNotified(t *testing.T) {
	admin := &eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_ADMIN, UserId: new(uuid.NewString())}
	e := event(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER, admin)
	e.CourierId, e.PreviousCourierId = nil, new(courierID.String())
	got := recipients(e)
	if len(got) != 2 || got[0].role != requester || got[1] != (recipient{courierID.String(), previousCourier}) {
		t.Fatalf("recipients = %+v", got)
	}
	if text := message(e, previousCourier); text != "You've been unassigned from the order from Fine Food. You don't need to pick it up." {
		t.Fatalf("message = %q", text)
	}

	// A courier who withdraws caused the change, so they aren't told.
	e.Actor = user(courierID)
	if got := recipients(e); len(got) != 1 || got[0].role != requester {
		t.Fatalf("withdrawal recipients = %+v", got)
	}
}

func TestMessages(t *testing.T) {
	s := func(v eventsv1.OrderStatus) eventsv1.OrderStatus { return v }
	cases := []struct {
		previous, status eventsv1.OrderStatus
		role             role
		want             string
	}{
		{s(eventsv1.OrderStatus_ORDER_STATUS_PENDING_RESERVATION), s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER), requester, "Your order from Fine Food is open. We'll tell you when a courier accepts it."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP), s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER), requester, "Your courier is no longer assigned to your order from Fine Food. It's open for other couriers again."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER), s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP), requester, "A courier accepted your order from Fine Food."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER), s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP), courier, "Your offer was accepted. Head to the pickup location for the order you're delivering from Fine Food."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING), s(eventsv1.OrderStatus_ORDER_STATUS_DELIVERED), requester, "Your order from Fine Food has been delivered. Please confirm receipt in the FoC app."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_DELIVERED), s(eventsv1.OrderStatus_ORDER_STATUS_COMPLETED), courier, "The requester confirmed receipt of the order you're delivering from Fine Food. Thanks for delivering!"},
		{s(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING), s(eventsv1.OrderStatus_ORDER_STATUS_CANCELLED), courier, "The order you're delivering from Fine Food was cancelled after pickup. Please record where you left the item in the FoC app."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP), s(eventsv1.OrderStatus_ORDER_STATUS_CANCELLED), courier, "The order you're delivering from Fine Food was cancelled."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER), s(eventsv1.OrderStatus_ORDER_STATUS_EXPIRED), requester, "Your order from Fine Food expired before a courier accepted it."},
		{s(eventsv1.OrderStatus_ORDER_STATUS_PENDING_RESERVATION), s(eventsv1.OrderStatus_ORDER_STATUS_REJECTED), requester, "Your order from Fine Food couldn't be opened because the reward couldn't be reserved."},
		// No message: creation, and changes the courier needn't hear about.
		{s(eventsv1.OrderStatus_ORDER_STATUS_UNSPECIFIED), s(eventsv1.OrderStatus_ORDER_STATUS_PENDING_RESERVATION), requester, ""},
		{s(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING), s(eventsv1.OrderStatus_ORDER_STATUS_DELIVERED), courier, ""},
	}
	for _, tc := range cases {
		got := message(event(tc.previous, tc.status, system), tc.role)
		if got != tc.want {
			t.Errorf("%s -> %s (%d) = %q, want %q", tc.previous, tc.status, tc.role, got, tc.want)
		}
	}
}
