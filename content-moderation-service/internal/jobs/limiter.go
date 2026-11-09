package jobs

import "context"

// Limiter is a bounded in-flight gate shared by the worker pool and the
// configuration test operation, so a configuration test never runs alongside
// more than the configured number of live assessments.
type Limiter struct {
	ch chan struct{}
}

func NewLimiter(n int) *Limiter {
	if n < 1 {
		n = 1
	}
	return &Limiter{ch: make(chan struct{}, n)}
}

func (l *Limiter) Acquire(ctx context.Context) error {
	select {
	case l.ch <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (l *Limiter) Release() {
	select {
	case <-l.ch:
	default:
	}
}

func (l *Limiter) TryAcquire() bool {
	select {
	case l.ch <- struct{}{}:
		return true
	default:
		return false
	}
}

func (l *Limiter) InFlight() int { return len(l.ch) }
