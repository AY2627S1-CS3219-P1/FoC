package order

// Transition checks lifecycle and persisted relationships only. Operations must
// separately enforce Credit, eligibility, proof and deadline prerequisites.
// Repeats are deliberately limited to the two actions whose stored state proves
// the same participant already succeeded. Upload retries need their own identity.
func Transition(o Order, action Action, actor Actor) (next Status, repeat bool, err error) {
	if !o.Status.Valid() {
		return "", false, ErrPrecondition
	}
	if actor.Kind != System && ((actor.Kind != User && actor.Kind != Admin) || actor.ID == "") {
		return "", false, ErrForbidden
	}
	requester := actor.Kind == User && o.Requester(actor.ID)
	courier := actor.Kind == User && o.Courier(actor.ID)
	if action == Assign && actor.Kind == User && o.Requester(actor.ID) {
		return "", false, ErrForbidden
	}
	if action == Assign && o.Status == AwaitingPickup && courier {
		return o.Status, true, nil
	}
	if action == ConfirmReceipt && o.Status == Completed && requester {
		return o.Status, true, nil
	}
	if o.Status.Terminal() {
		return "", false, ErrPrecondition
	}
	if allowedAction(o.Status, action, actor.Kind, requester, courier) {
		destinations := map[Action]Status{ConfirmReservation: AwaitingCourier, RefuseReservation: Rejected, Assign: AwaitingPickup, Cancel: Cancelled, Expire: Expired, Pickup: Delivering, Withdraw: AwaitingCourier, Unassign: AwaitingCourier, Deliver: Delivered, Timeout: Failed, ConfirmReceipt: Completed}
		if action == Cancel && courier {
			return Failed, false, nil
		}
		return destinations[action], false, nil
	}
	// Wrong actors on otherwise legal actions are authorization failures.
	for _, kind := range []Actor{{Kind: System}, {Kind: Admin, ID: "admin"}, {Kind: User, ID: o.RequesterID}} {
		if allowedAction(o.Status, action, kind.Kind, o.Requester(kind.ID), o.Courier(kind.ID)) {
			return "", false, ErrForbidden
		}
	}
	if allowedAction(o.Status, action, User, false, true) {
		return "", false, ErrForbidden
	}
	return "", false, ErrPrecondition
}
func allowedAction(s Status, a Action, k ActorKind, requester, courier bool) bool {
	switch s {
	case PendingReservation:
		return k == System && (a == ConfirmReservation || a == RefuseReservation)
	case AwaitingCourier:
		return (a == Assign && k == User && !requester) || (a == Cancel && (requester || k == Admin)) || (a == Expire && k == System)
	case AwaitingPickup:
		return (a == Pickup && courier) || (a == Cancel && (requester || k == Admin)) || (a == Withdraw && courier) || (a == Unassign && k == Admin)
	case Delivering:
		return (a == Deliver && courier) || (a == Cancel && (requester || courier)) || (a == Timeout && k == System)
	case Delivered:
		return a == ConfirmReceipt && requester
	}
	return false
}
