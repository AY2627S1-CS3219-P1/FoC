package job

import (
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"math/rand"
	"time"
)

const (
	defaultInitialDelay  = 10 * time.Second
	defaultMaxDelay      = 5 * time.Minute
	defaultMaxAttempts   = 5
	defaultInvalidBudget = 2
	defaultFilterBudget  = 2
)

type RetrySettings struct {
	InitialDelay  time.Duration
	MaxDelay      time.Duration
	MaxAttempts   int
	InvalidBudget int
	FilterBudget  int
}

func DefaultRetrySettings() RetrySettings {
	return RetrySettings{
		InitialDelay:  defaultInitialDelay,
		MaxDelay:      defaultMaxDelay,
		MaxAttempts:   defaultMaxAttempts,
		InvalidBudget: defaultInvalidBudget,
		FilterBudget:  defaultFilterBudget,
	}
}

func RetrySettingsFromConfig(maxAttempts int, initialDelay, maxDelay time.Duration) RetrySettings {
	policy := DefaultRetrySettings()
	policy.MaxAttempts = maxAttempts
	policy.InitialDelay = initialDelay
	policy.MaxDelay = maxDelay
	return policy
}

type decision struct {
	retry bool
	// Zero for terminal failure; bounded by MaxDelay otherwise.
	wait time.Duration
}

// Counts include the current failed attempt.
func (p RetrySettings) decide(
	kind moderation.ErrorKind,
	attemptCount, classCount int,
	rng *rand.Rand,
) decision {
	if attemptCount >= p.MaxAttempts {
		return decision{retry: false}
	}
	switch kind {
	case moderation.KindInvalidOutput:
		if classCount >= p.InvalidBudget {
			return decision{retry: false}
		}
		return decision{retry: true, wait: backoffDelay(classCount, p.InitialDelay, p.InitialDelay, rng)}
	case moderation.KindContentFilter:
		if classCount >= p.FilterBudget {
			return decision{retry: false}
		}
		return decision{retry: true, wait: backoffDelay(classCount, p.InitialDelay, p.InitialDelay, rng)}
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
