package reservation

import (
	"math/big"
	"time"
)

// PayoutRule returns how much of the accepted offer the courier is paid for an
// outcome that pays a courier. Credit applies the lateness rule today. If
// Order later computes the charge, a rule that reads it from the outcome
// replaces this one; settlement itself does not change.
type PayoutRule func(offer int64, o OrderOutcome) int64

// LatenessPayout pays the whole offer, reduced by the lateness rule for
// completed orders (C1.2.4–C1.2.7).
func LatenessPayout(offer int64, o OrderOutcome) int64 {
	if o.Type != OrderCompleted {
		return offer
	}
	return Payable(offer, o.DeliveredAt.Sub(o.PickedUpAt), o.DeliveryDuration)
}

// Payable applies the lateness rule to reward R, with elapsed = delivered -
// picked up and T = allowed:
//   - elapsed <= T pays R (C1.2.4);
//   - T < elapsed < 2T pays floor(R * (2T - elapsed) / T) (C1.2.5);
//   - elapsed >= 2T pays 0 (C1.2.7).
//
// allowed must be positive.
func Payable(reward int64, elapsed, allowed time.Duration) int64 {
	switch {
	case elapsed <= allowed:
		return reward
	case elapsed >= 2*allowed:
		return 0
	}
	n := new(big.Int).Mul(big.NewInt(reward), big.NewInt(int64(2*allowed-elapsed)))
	return n.Quo(n, big.NewInt(int64(allowed))).Int64()
}
