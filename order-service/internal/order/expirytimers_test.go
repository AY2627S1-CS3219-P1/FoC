package order

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Sources: R1.6.11 and the agreed original-expiry, actual-closure-time,
// startup/retry and atomic-outcome requirements. No delivery timeout policy is
// exercised here: durable delivery submissions require the proof handoff.
func TestExpiryTimerDeadlineAndFacts(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 20, 0, 0, time.UTC)
	for _, deadline := range []time.Time{now, now.Add(-20 * time.Minute)} {
		t.Run(deadline.String(), func(t *testing.T) {
			before := expiryTestOrder(1, deadline)
			r := newExpiryTimerFake(before)
			r.offers[before.ID] = []Offer{
				{ID: uuid.NewString(), OrderID: before.ID, Status: OfferOpen},
				{ID: uuid.NewString(), OrderID: before.ID, Status: OfferAccepting},
				{ID: uuid.NewString(), OrderID: before.ID, Status: OfferAccepted},
			}
			s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
			if err := s.Expire(t.Context(), before.ID); err != nil {
				t.Fatal(err)
			}

			want := cloneOrder(before)
			reason := "request_expired"
			want.Status, want.Revision = Expired, before.Revision+1
			want.UpdatedAt, want.ClosedAt, want.CloseReason = now, &now, &reason
			if !reflect.DeepEqual(r.orders[before.ID], want) {
				t.Fatalf("expiry changed unrelated fields: %+v", r.orders[before.ID])
			}
			change := r.changes[0]
			if change.Action != Expire || change.Actor != (Actor{Kind: System}) || !change.Audit.At.Equal(now) {
				t.Fatalf("wrong system audit: %+v", change)
			}
			if len(change.Facts) != 2 || change.Facts[0].Kind != StatusChanged || change.Facts[1].Kind != Outcome {
				t.Fatalf("wrong mandatory facts: %+v", change.Facts)
			}
			var fact FactsPayload
			if err := json.Unmarshal(change.Facts[1].Payload, &fact); err != nil {
				t.Fatal(err)
			}
			if fact.Status != Expired || fact.Action != Expire || fact.ReservedCredits != before.ReservedCredits || !fact.At.Equal(now) {
				t.Fatalf("wrong outcome observations: %+v", fact)
			}
			if len(change.Offers) != 2 || r.offers[before.ID][0].Status != OfferVoid || r.offers[before.ID][1].Status != OfferVoid || r.offers[before.ID][2].Status != OfferAccepted {
				t.Fatal("expiry did not void active offers while retaining historical acceptance")
			}
			if err := s.Expire(t.Context(), before.ID); err != nil || len(r.changes) != 1 {
				t.Fatalf("repeat duplicated outcome: %v %+v", err, r.changes)
			}
		})
	}
}

func TestExpiryTimerLeavesIneligibleOrdersUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	for _, status := range []Status{PendingReservation, AwaitingPickup, Delivering, Delivered, Completed, Cancelled, Expired, Failed, Rejected, AwaitingCourier} {
		t.Run(string(status), func(t *testing.T) {
			o := expiryTestOrder(1, now.Add(-time.Hour))
			o.Status = status
			if status == AwaitingCourier {
				o.ExpiresAt = now.Add(time.Microsecond)
			}
			r := newExpiryTimerFake(o)
			s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
			if err := s.Expire(t.Context(), o.ID); err != nil {
				t.Fatal(err)
			}
			if len(r.changes) != 0 || !reflect.DeepEqual(o, r.orders[o.ID]) {
				t.Fatal("ineligible order was changed")
			}
		})
	}
}

func TestExpiryTimerRechecksStateAndClockAfterLock(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	for _, assignmentWins := range []bool{false, true} {
		t.Run(fmt.Sprint(assignmentWins), func(t *testing.T) {
			o := expiryTestOrder(1, now.Add(time.Minute))
			r := newExpiryTimerFake(o)
			r.onLock = func(id string) {
				now = o.ExpiresAt
				if assignmentWins {
					assigned := r.orders[id]
					assigned.Status = AwaitingPickup
					r.orders[id] = assigned
				}
			}
			s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
			if err := s.Expire(t.Context(), o.ID); err != nil {
				t.Fatal(err)
			}
			want := Expired
			if assignmentWins {
				want = AwaitingPickup
			}
			if r.orders[o.ID].Status != want {
				t.Fatalf("stale eligibility: %+v", r.orders[o.ID])
			}
		})
	}
}

func TestExpiryTimerRollbackAndRestart(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	o := expiryTestOrder(1, now.Add(-time.Hour))
	r := newExpiryTimerFake(o)
	failure := errors.New("database unavailable")
	r.failID, r.failure = o.ID, failure
	s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
	if err := s.Expire(t.Context(), o.ID); !errors.Is(err, failure) {
		t.Fatalf("failure lost: %v", err)
	}
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatalf("candidate failure must not stop discovery: %v", err)
	}
	if len(r.changes) != 0 || !reflect.DeepEqual(r.orders[o.ID], o) {
		t.Fatal("failed commit left a partial expiry")
	}

	r.failID = ""
	restarted := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
	if err := restarted.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 1 || r.orders[o.ID].Status != Expired {
		t.Fatal("restart did not recover persisted overdue work")
	}
}

func TestExpiryTimerFailedCandidateDoesNotStarveNextPage(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	r := newExpiryTimerFake()
	for i := 1; i <= expiryTimerBatchSize+1; i++ {
		o := expiryTestOrder(i, now)
		r.orders[o.ID] = o
	}
	first, last := expiryTestOrder(1, now).ID, expiryTestOrder(expiryTimerBatchSize+1, now).ID
	r.failID, r.failure = first, errors.New("broken order")
	s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.orders[last].Status != AwaitingCourier {
		t.Fatal("first scan exceeded its batch bound")
	}
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.orders[last].Status != Expired || r.orders[first].Status != AwaitingCourier {
		t.Fatal("failed earlier candidate starved the next page")
	}
	r.failID = ""
	now = now.Add(time.Minute)
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != expiryTimerBatchSize+1 || r.orders[first].Status != Expired {
		t.Fatal("new sweep did not retry the failed candidate")
	}
}

func TestExpiryTimerRunsAtStartupAndStopsOnCancellation(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	o := expiryTestOrder(1, now)
	r := newExpiryTimerFake(o)
	r.applied = make(chan struct{}, 1)
	s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()
	select {
	case <-r.applied:
	case <-time.After(time.Second):
		t.Fatal("startup did not scan immediately")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop on cancellation")
	}
}

// Source: healthy orders must continue on the normal scan cadence while a
// different order is deferred for retry. Retry bookkeeping is not durable work.
func TestExpiryTimerRetryDoesNotDelayHealthyCandidates(t *testing.T) {
	now := time.Date(2026, 10, 20, 14, 0, 0, 0, time.UTC)
	broken := expiryTestOrder(1, now)
	healthy := expiryTestOrder(2, now.Add(500*time.Millisecond))
	r := newExpiryTimerFake(broken, healthy)
	r.failID, r.failure = broken.ID, errors.New("persistent order failure")
	attempts := 0
	r.onLock = func(id string) {
		if id == broken.ID {
			attempts++
		}
	}
	s := NewExpiryTimers(r, ExpiryTimerOptions{Now: func() time.Time { return now }})
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	now = healthy.ExpiresAt
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.orders[healthy.ID].Status != Expired || attempts != 1 {
		t.Fatalf("healthy order delayed or broken candidate retried without backoff: healthy=%s attempts=%d", r.orders[healthy.ID].Status, attempts)
	}
	now = now.Add(time.Minute)
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 {
		t.Fatalf("failed candidate did not become retryable: %d", attempts)
	}
	// A cancellation on another instance removes this order from discovery.
	changed := r.orders[broken.ID]
	changed.Status = Cancelled
	r.orders[broken.ID] = changed
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(s.retries) != 0 {
		t.Fatal("obsolete retry state retained after a complete sweep")
	}
}

func TestExpiryTimerRunRecoversTransientFailure(t *testing.T) {
	now := time.Now().UTC()
	o := expiryTestOrder(1, now.Add(-time.Hour))
	r := newExpiryTimerFake(o)
	r.failID, r.failure = o.ID, errors.New("temporary write failure")
	attempted := make(chan struct{}, 1)
	r.onLock = func(string) {
		select {
		case attempted <- struct{}{}:
		default:
		}
	}
	r.applied = make(chan struct{}, 1)
	s := NewExpiryTimers(r, ExpiryTimerOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-attempted:
	case <-time.After(3 * time.Second):
		t.Fatal("no initial attempt")
	}
	r.mu.Lock()
	r.failID = ""
	r.mu.Unlock()
	select {
	case <-r.applied:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not retry the transient failure")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retrying worker did not stop")
	}
}

type blockedExpiryRepository struct {
	ExpiryTimerRepository
	started chan struct{}
}

func (r *blockedExpiryRepository) Within(ctx context.Context, _ func(Tx) error) error {
	close(r.started)
	<-ctx.Done()
	return ctx.Err()
}

func TestExpiryTimerCancellationInterruptsBlockedAttempt(t *testing.T) {
	now := time.Now().UTC()
	r := &blockedExpiryRepository{ExpiryTimerRepository: newExpiryTimerFake(expiryTestOrder(1, now.Add(-time.Hour))), started: make(chan struct{})}
	s := NewExpiryTimers(r, ExpiryTimerOptions{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("no transaction attempt")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the blocked transaction")
	}
}

func expiryTestOrder(n int, deadline time.Time) Order {
	return Order{
		ID:               fmt.Sprintf("00000000-0000-4000-8000-%012d", n),
		RequesterID:      "10000000-0000-4000-8000-000000000001",
		Status:           AwaitingCourier,
		RewardCredits:    20,
		ReservedCredits:  20,
		DeliveryDuration: 20 * time.Minute,
		ExpiresAt:        deadline,
		Revision:         2,
		CreatedAt:        deadline.Add(-time.Hour),
		UpdatedAt:        deadline.Add(-time.Hour),
	}
}

type expiryTimerFake struct {
	mu      sync.Mutex
	orders  map[string]Order
	offers  map[string][]Offer
	changes []Change
	failID  string
	failure error
	onLock  func(string)
	applied chan struct{}
}

func newExpiryTimerFake(orders ...Order) *expiryTimerFake {
	r := &expiryTimerFake{orders: map[string]Order{}, offers: map[string][]Offer{}}
	for _, o := range orders {
		r.orders[o.ID] = o
	}
	return r
}

func (r *expiryTimerFake) DueExpiryTimers(_ context.Context, now time.Time, after string, limit int32) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, o := range r.orders {
		if id > after && o.Status == AwaitingCourier && !o.ExpiresAt.After(now) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids[:min(len(ids), int(limit))], nil
}

func (r *expiryTimerFake) Within(ctx context.Context, fn func(Tx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	tx := &expiryTimerFakeTx{repo: r}
	if err := fn(tx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if tx.change == nil {
		return nil
	}
	c := *tx.change
	r.orders[c.Order.ID] = c.Order
	for _, change := range c.Offers {
		for i, offer := range r.offers[c.Order.ID] {
			if offer.ID == change.Offer.ID {
				r.offers[c.Order.ID][i] = change.Offer
			}
		}
	}
	r.changes = append(r.changes, c)
	if r.applied != nil {
		r.applied <- struct{}{}
	}
	return nil
}

type expiryTimerFakeTx struct {
	repo   *expiryTimerFake
	locked *Order
	change *Change
}

func (tx *expiryTimerFakeTx) LockOrder(_ context.Context, id string) (Order, error) {
	if tx.repo.onLock != nil {
		tx.repo.onLock(id)
	}
	o, ok := tx.repo.orders[id]
	if !ok {
		return Order{}, ErrNotFound
	}
	tx.locked = &o
	return cloneOrder(o), nil
}

func (tx *expiryTimerFakeTx) Relationships(_ context.Context, id string) ([]Offer, error) {
	if tx.locked == nil || tx.locked.ID != id {
		return nil, ErrPrecondition
	}
	return slices.Clone(tx.repo.offers[id]), nil
}

func (tx *expiryTimerFakeTx) Apply(_ context.Context, c Change) error {
	if c.Order.ID == tx.repo.failID {
		return tx.repo.failure
	}
	if err := validateChange(tx.locked, c); err != nil {
		return err
	}
	tx.change = &c
	return nil
}
