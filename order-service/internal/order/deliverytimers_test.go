package order

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Sources: R1.6.10, agreed Q1/Q3/Q7-Q9, and B3's zero-payable timeout
// warning contract. Proof eligibility is the B2-owned predicate, not a copy
// of its schema/validation rules in the timer.
func TestDeliveryTimerDeadlineAndAtomicFacts(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	for _, delay := range []time.Duration{0, 20 * time.Minute} {
		t.Run(delay.String(), func(t *testing.T) {
			before := deliveryTimerTestOrder(now.Add(-delay))
			r := newDeliveryTimerFake(before)
			s := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }})
			if err := s.Timeout(t.Context(), before.ID); err != nil {
				t.Fatal(err)
			}

			got := r.orders[before.ID]
			if got.CloseReason == nil || *got.CloseReason == "" {
				t.Fatal("missing timeout close reason")
			}
			want := cloneOrder(before)
			want.Status, want.Revision = Failed, before.Revision+1
			want.UpdatedAt, want.ClosedAt, want.CloseReason = now, &now, got.CloseReason
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("timeout changed unrelated observations: %+v", got)
			}
			change := r.changes[0]
			if change.Actor != (Actor{Kind: System}) || change.Action != Timeout || !change.Audit.At.Equal(now) {
				t.Fatalf("wrong System timeout audit: %+v", change)
			}
			kinds := make(map[FactKind]int)
			for _, fact := range change.Facts {
				kinds[fact.Kind]++
				var p FactsPayload
				if err := json.Unmarshal(fact.Payload, &p); err != nil {
					t.Fatal(err)
				}
				if p.Status != Failed || p.Action != Timeout || !p.At.Equal(now) || p.ReservedCredits != before.ReservedCredits || p.DeliveredAt != nil {
					t.Fatalf("wrong timeout observations: %+v", p)
				}
			}
			if kinds[StatusChanged] != 1 || kinds[Outcome] != 1 || kinds[CourierWarning] != 1 || len(change.Facts) != 3 {
				t.Fatalf("missing/duplicate timeout facts: %+v", kinds)
			}
			if err := s.Timeout(t.Context(), before.ID); err != nil || len(r.changes) != 1 {
				t.Fatalf("repeat duplicated timeout: %v", err)
			}
		})
	}
}

func TestDeliveryTimerProtectsPendingSubmissionUntilRejected(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	o := deliveryTimerTestOrder(now.Add(-time.Hour))
	r := newDeliveryTimerFake(o)
	r.pending = true
	s := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }})
	for range 3 {
		if err := s.ScanOnce(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.changes) != 0 || !reflect.DeepEqual(r.orders[o.ID], o) {
		t.Fatal("pending on-time confirmation did not protect the order")
	}
	// B2 has rejected the immutable submission. No new receipt is fabricated.
	r.pending = false
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if r.orders[o.ID].Status != Failed || len(r.changes) != 1 {
		t.Fatal("rejected proof prevented overdue timeout")
	}
}

func TestDeliveryTimerRechecksParentAndPendingAfterLock(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	for _, status := range []Status{Delivering, Delivered, Completed, Cancelled, Failed} {
		t.Run(string(status), func(t *testing.T) {
			o := deliveryTimerTestOrder(now)
			r := newDeliveryTimerFake(o)
			r.onLock = func(id string) {
				changed := r.orders[id]
				changed.Status = status
				r.orders[id] = changed
				r.pending = true
			}
			s := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }})
			if err := s.Timeout(t.Context(), o.ID); err != nil {
				t.Fatal(err)
			}
			if len(r.changes) != 0 || r.orders[o.ID].Status != status {
				t.Fatal("stale timeout overrode confirmation/cancellation/pending admission")
			}
			if status == Delivering && r.pendingReads != 1 {
				t.Fatal("pending state was not read after locking")
			}
			if status != Delivering && r.pendingReads != 0 {
				t.Fatal("terminal order unnecessarily consulted pending records")
			}
		})
	}
}

func TestDeliveryTimerLeavesEarlyAndNonDeliveringOrdersUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	for _, status := range []Status{Delivering, PendingReservation, AwaitingCourier, AwaitingPickup, Delivered, Completed, Cancelled, Failed, Expired, Rejected} {
		t.Run(string(status), func(t *testing.T) {
			o := deliveryTimerTestOrder(now.Add(time.Microsecond))
			o.Status = status
			r := newDeliveryTimerFake(o)
			s := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }})
			if err := s.Timeout(t.Context(), o.ID); err != nil {
				t.Fatal(err)
			}
			if len(r.changes) != 0 || !reflect.DeepEqual(r.orders[o.ID], o) || r.pendingReads != 0 {
				t.Fatal("ineligible order changed or consulted proof storage")
			}
		})
	}
}

func TestDeliveryTimerGuardAndCommitFailuresLeaveOrderRetryable(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	for _, failGuard := range []bool{false, true} {
		t.Run(map[bool]string{false: "commit", true: "pending read"}[failGuard], func(t *testing.T) {
			o := deliveryTimerTestOrder(now)
			r := newDeliveryTimerFake(o)
			failure := errors.New("temporary database failure")
			if failGuard {
				r.guardError = failure
			} else {
				r.failID, r.failure = o.ID, failure
			}
			s := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }})
			if err := s.Timeout(t.Context(), o.ID); !errors.Is(err, failure) {
				t.Fatalf("failure hidden: %v", err)
			}
			if len(r.changes) != 0 || !reflect.DeepEqual(r.orders[o.ID], o) {
				t.Fatal("failure left partial terminal facts")
			}
			r.guardError, r.failID = nil, ""
			if err := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }}).ScanOnce(t.Context()); err != nil {
				t.Fatal(err)
			}
			if r.orders[o.ID].Status != Failed || len(r.changes) != 1 {
				t.Fatal("restart could not recover overdue timeout")
			}
		})
	}
}

func TestDeliveryTimerPendingFrontPageDoesNotStarveNextPage(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	r := newDeliveryTimerFake()
	r.pendingIDs = make(map[string]bool)
	for i := 1; i <= deliveryTimerBatchSize+1; i++ {
		o := deliveryTimerTestOrder(now)
		o.ID = expiryTestOrder(i, now).ID
		r.orders[o.ID] = o
		r.pendingIDs[o.ID] = i <= deliveryTimerBatchSize
	}
	s := NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }})
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 0 {
		t.Fatal("front-page pending submissions were timed out")
	}
	if err := s.ScanOnce(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 1 || r.orders[expiryTestOrder(deliveryTimerBatchSize+1, now).ID].Status != Failed {
		t.Fatal("pending front page starved unprotected later order")
	}
}

func TestDeliveryTimerConcurrentInstancesWriteOneOutcome(t *testing.T) {
	now := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	o := deliveryTimerTestOrder(now)
	r := newDeliveryTimerFake(o)
	var workers sync.WaitGroup
	failures := make(chan error, 50)
	for range 50 {
		workers.Go(func() {
			failures <- NewDeliveryTimers(r, DeliveryTimerOptions{TestNow: func() time.Time { return now }}).Timeout(t.Context(), o.ID)
		})
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(r.changes) != 1 || r.orders[o.ID].Status != Failed {
		t.Fatal("contenders committed multiple timeout revisions")
	}
}

func deliveryTimerTestOrder(deadline time.Time) Order {
	o := expiryTestOrder(1, deadline.Add(-time.Hour))
	courier := uuid.NewString()
	assigned := deadline.Add(-time.Hour)
	picked := deadline.Add(-40 * time.Minute)
	o.Status, o.CourierID = Delivering, &courier
	o.AssignedAt, o.PickedUpAt = &assigned, &picked
	o.DeliveryDuration = 20 * time.Minute
	return o
}

type deliveryTimerFake struct {
	*expiryTimerFake
	pending      bool
	pendingIDs   map[string]bool
	guardError   error
	serverTime   time.Time
	clockError   error
	pendingReads int
}

func newDeliveryTimerFake(orders ...Order) *deliveryTimerFake {
	return &deliveryTimerFake{expiryTimerFake: newExpiryTimerFake(orders...)}
}

func (r *deliveryTimerFake) WithinDeliveryTimers(ctx context.Context, fn func(DeliveryTimerTx) error) error {
	return r.Within(ctx, func(tx Tx) error {
		return fn(&deliveryTimerFakeTx{expiryTimerFakeTx: tx.(*expiryTimerFakeTx), repository: r})
	})
}

func (r *deliveryTimerFake) DueDeliveryTimers(_ context.Context, now time.Time, after string, limit int32) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var ids []string
	for id, o := range r.orders {
		deadline, err := deliveryTimeoutDeadline(o)
		if err == nil && id > after && o.Status == Delivering && !now.Before(deadline) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids[:min(len(ids), int(limit))], nil
}

type deliveryTimerFakeTx struct {
	*expiryTimerFakeTx
	repository *deliveryTimerFake
}

func (tx *deliveryTimerFakeTx) HasPendingDelivery(_ context.Context, id string) (bool, error) {
	if tx.locked == nil || tx.locked.ID != id {
		return false, ErrPrecondition
	}
	r := tx.repository
	r.pendingReads++
	return r.pending || r.pendingIDs[id], r.guardError
}

func (tx *deliveryTimerFakeTx) ProofServerTime(_ context.Context) (time.Time, error) {
	if tx.locked == nil {
		return time.Time{}, ErrPrecondition
	}
	return tx.repository.serverTime, tx.repository.clockError
}

// Source: use the same authoritative database clock as confirmation admission,
// so a fast process clock cannot fail an order that is not actually overdue.
func TestDeliveryTimerUsesLockedServerClock(t *testing.T) {
	deadline := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	o := deliveryTimerTestOrder(deadline)
	r := newDeliveryTimerFake(o)
	r.serverTime = deadline.Add(-time.Microsecond)
	s := NewDeliveryTimers(r, DeliveryTimerOptions{})
	if err := s.Timeout(t.Context(), o.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 0 {
		t.Fatal("process clock overrode the locked authoritative clock")
	}
	r.serverTime = deadline
	if err := s.Timeout(t.Context(), o.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.changes) != 1 || r.orders[o.ID].Status != Failed || !r.orders[o.ID].UpdatedAt.Equal(deadline) {
		t.Fatal("exact DB deadline was not applied")
	}
}

func TestDeliveryTimerClockFailureDoesNotFailOrder(t *testing.T) {
	deadline := time.Date(2026, 10, 21, 14, 0, 0, 0, time.UTC)
	o := deliveryTimerTestOrder(deadline)
	r := newDeliveryTimerFake(o)
	r.clockError = errors.New("database clock unavailable")
	s := NewDeliveryTimers(r, DeliveryTimerOptions{})
	if err := s.Timeout(t.Context(), o.ID); !errors.Is(err, r.clockError) {
		t.Fatalf("clock failure hidden: %v", err)
	}
	if len(r.changes) != 0 || !reflect.DeepEqual(r.orders[o.ID], o) {
		t.Fatal("clock failure produced partial timeout")
	}
}
