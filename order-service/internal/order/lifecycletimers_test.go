package order

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Sources: the agreed persisted startup/recovery, bounded attempts and
// cancellation-aware shutdown requirements. Both policies use this one scanner.
func TestLifecycleTimerRetriesFailedDiscovery(t *testing.T) {
	var discoveries atomic.Int32
	var attempts atomic.Int32
	applied := make(chan struct{}, 1)
	s := newLifecycleTimerScanner("test", nil,
		func(context.Context, time.Time, string, int32) ([]string, error) {
			if discoveries.Add(1) == 1 {
				return nil, errors.New("temporary discovery failure")
			}
			return []string{uuid.Nil.String()}, nil
		},
		func(ctx context.Context, _ string) error {
			if _, bounded := ctx.Deadline(); !bounded {
				return errors.New("unbounded attempt")
			}
			attempts.Add(1)
			select {
			case applied <- struct{}{}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-applied:
	case <-time.After(3 * time.Second):
		t.Fatal("discovery never recovered into a bounded attempt")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after recovery")
	}
	if discoveries.Load() < 2 || attempts.Load() < 1 {
		t.Fatalf("wrong recovery: discoveries=%d attempts=%d", discoveries.Load(), attempts.Load())
	}
}

func TestLifecycleTimerCancellationInterruptsBlockedDiscovery(t *testing.T) {
	started := make(chan bool, 1)
	s := newLifecycleTimerScanner("test", nil,
		func(ctx context.Context, _ time.Time, _ string, _ int32) ([]string, error) {
			_, bounded := ctx.Deadline()
			started <- bounded
			<-ctx.Done()
			return nil, ctx.Err()
		},
		func(context.Context, string) error { return errors.New("must not attempt after failed discovery") },
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case bounded := <-started:
		if !bounded {
			t.Error("discovery context has no deadline")
		}
	case <-time.After(time.Second):
		t.Fatal("startup did not enter discovery")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel blocked discovery")
	}
}
