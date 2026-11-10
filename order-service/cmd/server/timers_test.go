package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

// Source: agreed Q5/Q6, immediate startup discovery for both persisted deadlines,
// independently of external services, and cancellation-aware worker shutdown.
func TestLifecycleTimerStartupAndShutdown(t *testing.T) {
	r := &startupTimerRepository{discovered: make(chan string, 4)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	startLifecycleTimers(ctx, &workers, r)

	seen := make(map[string]bool)
	for len(seen) < 2 {
		select {
		case kind := <-r.discovered:
			seen[kind] = true
		case <-time.After(time.Second):
			t.Fatal("both timers did not discover persisted work at startup")
		}
	}
	if !seen["expiry"] || !seen["delivery_timeout"] {
		t.Fatalf("wrong timer workers: %+v", seen)
	}
	cancel()
	done := make(chan struct{})
	go func() {
		workers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("service cancellation did not join both timer workers")
	}
	if r.unexpected.Load() != 0 {
		t.Fatal("empty discovery attempted an order mutation")
	}
}

type startupTimerRepository struct {
	discovered chan string
	unexpected atomic.Int32
}

func (r *startupTimerRepository) DueExpiryTimers(context.Context, time.Time, string, int32) ([]string, error) {
	select {
	case r.discovered <- "expiry":
	default:
	}
	return nil, nil
}

func (r *startupTimerRepository) DueDeliveryTimers(context.Context, time.Time, string, int32) ([]string, error) {
	select {
	case r.discovered <- "delivery_timeout":
	default:
	}
	return nil, nil
}

func (r *startupTimerRepository) WithinExpiryTimers(context.Context, func(order.ExpiryTimerTx) error) error {
	r.unexpected.Add(1)
	return errors.New("empty discovery must not mutate an order")
}

func (r *startupTimerRepository) WithinDeliveryTimers(context.Context, func(order.DeliveryTimerTx) error) error {
	r.unexpected.Add(1)
	return errors.New("empty discovery must not mutate an order")
}
