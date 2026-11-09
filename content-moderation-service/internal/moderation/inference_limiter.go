package moderation

import "context"

// InferenceLimiter is a bounded in-flight gate shared by the worker pool and the
// configuration test operation, so a configuration test never runs alongside
// more than the configured number of live assessments.
type InferenceLimiter struct {
	ch chan struct{}
}

func NewInferenceLimiter(n int) *InferenceLimiter {
	if n < 1 {
		n = 1
	}
	return &InferenceLimiter{ch: make(chan struct{}, n)}
}

func (l *InferenceLimiter) Acquire(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *InferenceLimiter) Release() {
	select {
	case <-l.ch:
	default:
	}
}

func (l *InferenceLimiter) TryAcquire() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *InferenceLimiter) InFlight() int { return len(l.ch) }
