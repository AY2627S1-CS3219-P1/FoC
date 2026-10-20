package reservation

import (
	"math/big"
	"time"
)

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
