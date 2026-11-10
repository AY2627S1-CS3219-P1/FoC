package notify

import (
	"fmt"
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

// delivery says how a notification is handled when it can't be sent promptly.
type delivery int

const (
	// alwaysDeliver is sent however late, such as an order's outcome.
	alwaysDeliver delivery = iota
	// dropIfLate is sent only within LateWindow of the change.
	dropIfLate
	// unlessReassigned is sent only while the removed courier is not currently assigned.
	unlessReassigned
	// ifStillCurrent is sent only while the change is the order's latest.
	ifStillCurrent
)

// deliveryFor classifies the notification message returns for one recipient.
func deliveryFor(e *eventsv1.OrderStatusChanged, r role) delivery {
	switch {
	case r == previousCourier:
		// Another courier may take over, but this courier may also accept again.
		return unlessReassigned
	case e.GetStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP && r == courier:
		return ifStillCurrent
	case e.GetStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER && r == requester &&
		e.GetPreviousStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP:
		return ifStillCurrent
	case e.GetStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
		e.GetStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		e.GetStatus() == eventsv1.OrderStatus_ORDER_STATUS_DELIVERING:
		return dropIfLate
	}
	return alwaysDeliver
}

// emailed reports whether the notification also goes by email to a recipient
// Telegram can't reach: outcomes and anything they must act on. Progress the
// app already shows stays Telegram-only.
func emailed(e *eventsv1.OrderStatusChanged, r role) bool {
	return deliveryFor(e, r) == alwaysDeliver || deliveryFor(e, r) == unlessReassigned ||
		(e.GetStatus() == eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP && r == courier)
}

// offerMessage tells the requester about a courier's offer.
func offerMessage(e *eventsv1.OrderOfferMade) string {
	order := "your order"
	if name := e.GetPickupLocationName(); name != "" {
		order += " from " + name
	}
	return fmt.Sprintf(
		"A courier offered %d credits to deliver %s. If the offer is still open, you can accept it in the FoC app.",
		e.GetCredits(),
		order,
	)
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
			return capitalize(
				order,
			) + " was cancelled after pickup. Please record where you left the item in the FoC app."
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
