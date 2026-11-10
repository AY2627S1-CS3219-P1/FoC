package job

import "context"

// InferenceLimiter bounds concurrent assessments before a worker claims a job.
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
