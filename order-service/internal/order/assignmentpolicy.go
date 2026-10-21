package order

// validateAssignmentOffers is the offer side of the aggregate authorization
// boundary. Expected status alone is insufficient: changes must refer to the
// stored courier/amount and to the initiating actor or immutable pending attempt.
func validateAssignmentOffers(before Order, stored []Offer, change Change, attempt *AssignmentAttempt) error {
	if change.Action != ProposeOffer && change.Action != BeginAssignment && change.Action != ResolveAssignment && change.Action != Assign && change.Action != AcceptCounteroffer {
		return nil
	}
	if change.Actor.Kind != User || change.Actor.ID == "" {
		return ErrForbidden
	}
	byID := make(map[string]Offer, len(stored))
	for _, f := range stored {
		byID[f.ID] = f
	}
	seen := make(map[string]bool, len(change.Offers))
	for _, update := range change.Offers {
		f := update.Offer
		if f.OrderID != before.ID || f.CourierID == before.RequesterID || f.Credits <= 0 || seen[f.ID] {
			return ErrInvalid
		}
		seen[f.ID] = true
		old, exists := byID[f.ID]
		if exists {
			if update.ExpectedStatus != old.Status || old.CourierID != f.CourierID || old.Credits != f.Credits || !old.CreatedAt.Equal(f.CreatedAt) {
				return ErrConflict
			}
		} else if update.ExpectedStatus != "" {
			return ErrConflict
		}
		switch change.Action {
		case ProposeOffer:
			if before.Status != AwaitingCourier || before.IsRequestedBy(change.Actor.ID) || f.CourierID != change.Actor.ID ||
				(!exists && (f.Status != OfferOpen || f.Credits <= before.RewardCredits)) ||
				(exists && (old.Status != OfferOpen || f.Status != OfferVoid)) {
				return ErrForbidden
			}
		case BeginAssignment:
			if !matchesAttempt(attempt, before, change.Actor) || f.ID != attempt.OfferID || f.CourierID != attempt.CourierID || f.Credits != attempt.Amount || f.Status != OfferAccepting {
				return ErrForbidden
			}
			if attempt.Method == AcceptOrderMethod {
				if exists || f.CourierID != change.Actor.ID || f.Credits != before.RewardCredits {
					return ErrForbidden
				}
			} else if !before.IsRequestedBy(change.Actor.ID) || !exists || old.Status != OfferOpen {
				return ErrForbidden
			}
		case ResolveAssignment:
			if !matchesAttempt(attempt, before, change.Actor) || f.ID != attempt.OfferID || !exists ||
				(old.Status != OfferAccepting && old.Status != OfferVoid) ||
				(f.Status != OfferOpen && f.Status != OfferVoid) || (old.Status == OfferVoid && f.Status != OfferVoid) {
				return ErrForbidden
			}
			if f.Status == OfferOpen && (attempt.Method == AcceptOrderMethod || attempt.Rejection != "insufficient_credits") {
				return ErrForbidden
			}
		case Assign, AcceptCounteroffer:
			if !matchesAttempt(attempt, before, change.Actor) || !exists {
				return ErrForbidden
			}
			if f.ID == attempt.OfferID {
				if old.Status != OfferAccepting || f.Status != OfferAccepted || f.CourierID != attempt.CourierID || f.Credits != attempt.Amount {
					return ErrForbidden
				}
			} else if old.Status != OfferOpen || f.Status != OfferVoid {
				return ErrForbidden
			}
		}
	}
	return nil
}

func matchesAttempt(attempt *AssignmentAttempt, before Order, actor Actor) bool {
	return attempt != nil && attempt.OrderID == before.ID && attempt.CallerID == actor.ID && actor.Kind == User
}
