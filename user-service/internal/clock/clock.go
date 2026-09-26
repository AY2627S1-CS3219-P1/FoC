// Package clock abstracts time so expiry rules (10-minute links, session
// TTL) are testable.
package clock

import (
	"sync"
	"time"
)

type Clock interface {
	Now() time.Time
}

type Real struct{}

func (Real) Now() time.Time { return time.Now().UTC() }

// Fake is a manually advanced clock for tests.
type Fake struct {
	mu sync.Mutex
	t  time.Time
}

func NewFake(t time.Time) *Fake { return &Fake{t: t.UTC()} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}
