package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
)

func TestRelayContinuesFullBatchesWithoutWaiting(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprintf("deferred=%v", deferred), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := &batchFacts{facts: runFacts(t, 151)}
				if deferred {
					for i := range 50 {
						store.facts[i].Version = 0
					}
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stopped := make(chan struct{})
				started := time.Now()
				go func() {
					defer close(stopped)
					New(store, &recordingPublisher{}).Run(ctx)
				}()

				synctest.Wait()
				want := 151
				if deferred {
					want -= 50
					if len(store.retried) != 50 {
						t.Errorf("deferred %d facts, want 50", len(store.retried))
					}
				}
				if len(store.published) != want || len(store.facts) != 0 {
					t.Errorf("published %d, want %d; %d facts remain", len(store.published), want, len(store.facts))
				}
				if elapsed := time.Since(started); elapsed != 0 {
					t.Errorf("waited %s with eligible facts queued", elapsed)
				}
				cancel()
				<-stopped
			})
		})
	}
}

type countedClaims struct {
	Repository
	calls atomic.Int64
	err   error
}

func (r *countedClaims) Claim(ctx context.Context, at, until time.Time) (*Fact, error) {
	r.calls.Add(1)
	if r.err != nil {
		return nil, r.err
	}
	return r.Repository.Claim(ctx, at, until)
}

func TestRelayWaitsAfterIdleOrStorageFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprintf("failed=%v", failed), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				store := &countedClaims{Repository: &batchFacts{}}
				if failed {
					store.err = errors.New("database unavailable")
				}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				stopped := make(chan struct{})
				go func() {
					defer close(stopped)
					New(store, &recordingPublisher{}).Run(ctx)
				}()

				synctest.Wait()
				if calls := store.calls.Load(); calls != 1 {
					t.Errorf("claimed %d times before the idle delay, want 1", calls)
				}
				time.Sleep(time.Second)
				synctest.Wait()
				if calls := store.calls.Load(); calls != 2 {
					t.Errorf("claimed %d times after the idle delay, want 2", calls)
				}
				cancel()
				<-stopped
			})
		})
	}
}

func runFacts(t *testing.T, count int) []Fact {
	t.Helper()
	facts := make([]Fact, count)
	for i := range facts {
		id := fmt.Sprintf("order-%d", i)
		payload, err := json.Marshal(app.FactsPayload{
			OrderID: id, Revision: 1, RequesterID: "requester",
			Status: app.PendingReservation, At: time.Now(),
		})
		if err != nil {
			t.Fatal(err)
		}
		facts[i] = Fact{
			ID: id, OrderID: id, Revision: 1, Version: 1,
			Kind: string(app.StatusChanged), Payload: payload, ActorKind: "system",
		}
	}
	return facts
}
