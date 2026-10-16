package notify

import (
	"strings"

	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
)

type role int

const (
	requester role = iota
	courier
	// The courier this change removed from the order.
	previousCourier
)

type recipient struct {
	userID string
	role   role
}

// recipients returns the participants who didn't cause the change. Admin and
// system changes reach both participants.
func recipients(e *eventsv1.OrderStatusChanged) []recipient {
	actor := ""
	if e.GetActor().GetType() == eventsv1.ActorType_ACTOR_TYPE_USER {
		actor = e.GetActor().GetUserId()
	}

	var out []recipient
	if id := e.GetRequesterId(); id != "" && !strings.EqualFold(id, actor) {
		out = append(out, recipient{id, requester})
	}
	if id := e.GetCourierId(); id != "" && !strings.EqualFold(id, actor) && !strings.EqualFold(id, e.GetRequesterId()) {
		out = append(out, recipient{id, courier})
	}
	if id := e.GetPreviousCourierId(); id != "" && e.GetCourierId() == "" && !strings.EqualFold(id, actor) {
		out = append(out, recipient{id, previousCourier})
	}
	return out
}

// message returns the text for one recipient, or "" when the change needs no
// message for them.
func message(e *eventsv1.OrderStatusChanged, r role) string {
	order := map[role]string{requester: "your order", courier: "the order you're delivering", previousCourier: "the order"}[r]
	if name := e.GetPickupLocationName(); name != "" {
		order += " from " + name
	}

	if r == previousCourier {
		if e.GetStatus() != eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER {
			return ""
		}
		return "You've been unassigned from " + order + ". You don't need to pick it up."
	}

	switch e.GetStatus() {
	case eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER:
		if r != requester {
			return ""
		}
		if e.GetPreviousStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP {
			return "Your courier is no longer assigned to " + order + ". It's open for other couriers again."
		}
		return capitalize(order) + " is open. We'll tell you when a courier accepts it."
	case eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP:
		if r == courier {
			return "Your offer was accepted. Head to the pickup location for " + order + "."
		}
		return "A courier accepted " + order + "."
	case eventsv1.OrderStatus_ORDER_STATUS_DELIVERING:
		return capitalize(order) + " has been picked up and is on its way."
	case eventsv1.OrderStatus_ORDER_STATUS_DELIVERED:
		if r != requester {
			return ""
		}
		return capitalize(order) + " has been delivered. Please confirm receipt in the FoC app."
	case eventsv1.OrderStatus_ORDER_STATUS_COMPLETED:
		if r == courier {
			return "The requester confirmed receipt of " + order + ". Thanks for delivering!"
		}
		return capitalize(order) + " is complete."
	case eventsv1.OrderStatus_ORDER_STATUS_CANCELLED:
		if r == courier && e.GetPreviousStatus() == eventsv1.OrderStatus_ORDER_STATUS_DELIVERING {
			return capitalize(order) + " was cancelled after pickup. Please record where you left the item in the FoC app."
		}
		return capitalize(order) + " was cancelled."
	case eventsv1.OrderStatus_ORDER_STATUS_EXPIRED:
		return capitalize(order) + " expired before a courier accepted it."
	case eventsv1.OrderStatus_ORDER_STATUS_FAILED:
		return "Delivery of " + order + " failed."
	case eventsv1.OrderStatus_ORDER_STATUS_REJECTED:
		return capitalize(order) + " couldn't be opened because the reward couldn't be reserved."
	}
	return ""
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
