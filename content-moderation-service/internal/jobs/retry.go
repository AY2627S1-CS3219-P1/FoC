package jobs

import (
	"math/rand"
	"time"
)

// Default retry policy parameters. Every value is an environment override; the
// constants record the defaults so the policy is explicit in one place.
const (
	defaultInitialDelay = 10 * time.Second
	defaultMaxDelay     = 5 * time.Minute
	defaultMaxAttempts  = 5
	// invalidBudget and filterBudget bound the small, non-exponential retry
	// budgets for invalid structured output and provider refusals.
	defaultInvalidBudget = 2
	defaultFilterBudget  = 2
)

// Policy configures how a failed attempt is rescheduled. It is a value type so
// it can be unit tested with a single generator and shared by the pool.
type Policy struct {
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	MaxAttempts   int
	InvalidBudget int
	FilterBudget  int
}

// DefaultPolicy returns the documented defaults.
func DefaultPolicy() Policy {
	return Policy{
		InitialDelay:  defaultInitialDelay,
		MaxDelay:      defaultMaxDelay,
		MaxAttempts:   defaultMaxAttempts,
		InvalidBudget: defaultInvalidBudget,
		FilterBudget:  defaultFilterBudget,
	}
}

// decision is the reschedule result for one failed attempt.
type decision struct {
	// retry is true when the job returns to the queue instead of erroring.
	retry bool
	// wait is the delay until the next attempt when retry is true; zero when
	// terminal. It never exceeds MaxDelay.
	wait time.Duration
}

// decide applies the retry policy to a failed attempt. attemptCount is the
// generation's attempt counter after this attempt; invalidCount and filterCount
// are the per-generation tallies for the two bounded budgets.
func (p Policy) decide(
	kind errorKind,
	attemptCount, invalidCount, filterCount int,
	rng *rand.Rand,
) decision {
	switch kind {
	case kindConfiguration:
		// Pause far enough in the future to avoid a retry storm, but never
		// exhaust the budget: a fixed configuration error cannot error the job.
		return decision{retry: true, wait: p.MaxDelay}
	case kindInvalidOutput:
		if invalidCount >= p.InvalidBudget {
			return decision{retry: false}
		}
		return decision{retry: true, wait: backoffDelay(invalidCount, p.InitialDelay, p.InitialDelay, rng)}
	case kindContentFilter:
		if filterCount >= p.FilterBudget {
			return decision{retry: false}
		}
		return decision{retry: true, wait: backoffDelay(filterCount, p.InitialDelay, p.InitialDelay, rng)}
	default:
		if attemptCount >= p.MaxAttempts {
			return decision{retry: false}
		}
		return decision{retry: true, wait: backoffDelay(attemptCount, p.InitialDelay, p.MaxDelay, rng)}
	}
}

// backoffDelay returns a capped exponential delay for the n-th attempt (1-based),
// with full jitter in [0, min(exp, max)]. A nil rng returns the capped exponent.
func backoffDelay(n int, initial, max time.Duration, rng *rand.Rand) time.Duration {
	if n < 1 {
		n = 1
	}
	exp := initial
	for i := 1; i < n; i++ {
		if exp > max/2 {
			exp = max
			break
		}
		exp *= 2
	}
	if exp > max || exp <= 0 {
		exp = max
	}
	if exp <= 0 {
		return 0
	}
	if rng == nil {
		return exp
	}
	return time.Duration(rng.Int63n(int64(exp) + 1))
}
