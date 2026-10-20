package order

// Transition checks lifecycle and persisted relationships only. Operations must
// separately enforce Credit, eligibility, proof and deadline prerequisites.
// Repeats are deliberately limited to the actions whose stored state proves the
// same participant already succeeded. Upload retries need their own identity.
func Transition(o Order, action Action, actor Actor) (next Status, repeat bool, err error) {
	if !o.Status.IsValid() {
		return "", false, ErrPrecondition
	}
	if actor.Kind != System && ((actor.Kind != User && actor.Kind != Admin) || actor.ID == "") {
		return "", false, ErrForbidden
	}

	if action == Pickup && o.PendingCancellationID != nil {
		return "", false, ErrPrecondition
	}
	if action == RemoveSuspendedCourier && (o.PendingCancellationID == nil || o.PickedUpAt != nil) {
		return "", false, ErrPrecondition
	}

	requester := actor.Kind == User && o.IsRequestedBy(actor.ID)
	courier := actor.Kind == User && o.IsAssignedTo(actor.ID)
	if action == Assign && actor.Kind == User && o.IsRequestedBy(actor.ID) {
		return "", false, ErrForbidden
	}

	// Only persisted evidence of the same participant's success proves a repeat.
	if action == Assign && o.Status == AwaitingPickup && courier {
		return o.Status, true, nil
	}
	// Only the assigned courier can reach Delivering, and the courier can't change
	// afterwards, so Delivering with the same courier proves their pickup.
	if action == Pickup && o.Status == Delivering && courier {
		return o.Status, true, nil
	}
	if action == ConfirmReceipt && o.Status == Completed && requester {
		return o.Status, true, nil
	}

	if o.Status.IsTerminal() {
		return "", false, ErrPrecondition
	}

	// Resolve an authorized action to its next state.
	if isActionAllowed(o.Status, action, actor.Kind, requester, courier) {
		destinations := map[Action]Status{
			ConfirmReservation:     AwaitingCourier,
			RefuseReservation:      Rejected,
			Assign:                 AwaitingPickup,
			AcceptCounteroffer:     AwaitingPickup,
			Cancel:                 Cancelled,
			Expire:                 Expired,
			Pickup:                 Delivering,
			Withdraw:               AwaitingCourier,
			Unassign:               AwaitingCourier,
			RemoveSuspendedCourier: AwaitingCourier,
			Deliver:                Delivered,
			Timeout:                Failed,
			ConfirmReceipt:         Completed,
		}
		if action == Cancel && courier {
			return Failed, false, nil
		}

		return destinations[action], false, nil
	}

	// Wrong actors on otherwise legal actions are authorization failures.
	for _, kind := range []Actor{{Kind: System}, {Kind: Admin, ID: "admin"}, {Kind: User, ID: o.RequesterID}} {
		if isActionAllowed(o.Status, action, kind.Kind, o.IsRequestedBy(kind.ID), o.IsAssignedTo(kind.ID)) {
			return "", false, ErrForbidden
		}
	}
	if isActionAllowed(o.Status, action, User, false, true) {
		return "", false, ErrForbidden
	}

	return "", false, ErrPrecondition
}

// isActionAllowed reports whether the actor may perform the action in this state.
func isActionAllowed(s Status, a Action, k ActorKind, requester, courier bool) bool {
	switch s {
	case PendingReservation:
		return k == System && (a == ConfirmReservation || a == RefuseReservation)
	case AwaitingCourier:
		return (a == Assign && k == User && !requester) || (a == AcceptCounteroffer && requester) || (a == Cancel && (requester || k == Admin)) || (a == Expire && k == System)
	case AwaitingPickup:
		return (a == Pickup && courier) || (a == Cancel && (requester || k == Admin)) || (a == Withdraw && courier) || (a == Unassign && k == Admin) || (a == RemoveSuspendedCourier && k == System)
	case Delivering:
		return (a == Deliver && courier) || (a == Cancel && (requester || courier)) || (a == Timeout && k == System)
	case Delivered:
		return a == ConfirmReceipt && requester
	}

	return false
}
