//go:build integration

package order

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// This scripted Credit contract fixture is not a Credit implementation. These
// tests exercise the real PostgreSQL aggregate/workflow boundary, not a ledger,
// workload authentication, RPC server, Kafka consumer or financial cross-service E2E.
type assignmentCreditFixture struct {
	mu                sync.Mutex
	views             map[string]AssignmentReservation
	refused, reverted map[string]bool
	available         int64
	denyReads         bool
	denyRevert        bool
	afterAdjust       func()
	adjusts, reverts  int
}

func newAssignmentCredit(available int64, orders ...Order) *assignmentCreditFixture {
	c := &assignmentCreditFixture{views: map[string]AssignmentReservation{}, refused: map[string]bool{}, reverted: map[string]bool{}, available: available}
	for _, o := range orders {
		c.views[o.ID] = AssignmentReservation{OrderID: o.ID, RequesterID: o.RequesterID, OriginalAmount: o.ReservedCredits, ReservedAmount: o.ReservedCredits, State: "held"}
	}
	return c
}

func (c *assignmentCreditFixture) Adjust(_ context.Context, in AdjustmentIntent) error {
	c.mu.Lock()
	c.adjusts++
	v := c.views[in.OrderID]
	if c.reverted[in.OfferID] || (v.State == "assigned" && v.OfferID != in.OfferID) {
		c.mu.Unlock()
		return ErrUnavailable
	}
	if c.refused[in.OfferID] {
		c.mu.Unlock()
		return ErrCreditInsufficient
	}
	if v.State == "assigned" && v.OfferID == in.OfferID {
		c.mu.Unlock()
		return nil
	}
	increase := in.Amount - v.ReservedAmount
	if increase > c.available {
		c.refused[in.OfferID] = true
		c.mu.Unlock()
		return ErrCreditInsufficient
	}
	c.available -= increase
	v.State, v.ReservedAmount, v.CourierID, v.OfferID = "assigned", in.Amount, in.CourierID, in.OfferID
	c.views[in.OrderID] = v
	hook := c.afterAdjust
	c.mu.Unlock()
	if hook != nil {
		hook()
	}
	return nil
}

func (c *assignmentCreditFixture) Revert(_ context.Context, orderID, attemptID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reverts++
	if c.denyRevert {
		return ErrUnavailable
	}
	c.reverted[attemptID] = true
	v := c.views[orderID]
	if v.State == "assigned" && v.OfferID == attemptID {
		c.available += v.ReservedAmount - v.OriginalAmount
		v.State, v.ReservedAmount, v.OfferID, v.CourierID = "held", v.OriginalAmount, "", ""
		c.views[orderID] = v
	}
	return nil
}

func (c *assignmentCreditFixture) AssignmentReservation(_ context.Context, orderID string) (AssignmentReservation, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.denyReads {
		return AssignmentReservation{}, ErrUnavailable
	}
	return c.views[orderID], nil
}

func assignmentOpenOrder(t *testing.T, f *fixture) Order {
	t.Helper()
	o := f.create(t)
	f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	o, err := f.repo.Get(f.ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func assignmentClock(o Order) (*atomic.Int64, func() time.Time) {
	clock := &atomic.Int64{}
	clock.Store(o.UpdatedAt.Add(time.Second).UnixNano())
	return clock, func() time.Time { return time.Unix(0, clock.Load()).UTC() }
}

// Source: R1.2.1, R1.2.2, NFR-02's 50 eligible couriers, and the approved
// lifetime UUID policy for both winning and rejected attempts.
func TestPostgresAssignmentContentionAndReplay(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	credit := newAssignmentCredit(70, o)
	// Keep the Order public while all 50 eligible requests contend. Requests
	// arriving after assignment are private-order admission failures instead.
	credit.afterAdjust = func() {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var count int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_assignment_attempts WHERE order_id=$1", o.ID).Scan(&count); err == nil && count == 50 {
				return
			}
			time.Sleep(time.Millisecond * 10)
		}
	}
	s := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	type answer struct {
		caller Caller
		key    string
		result AssignmentResult
		err    error
	}
	answers := make(chan answer, 50)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			<-start
			caller, key := Caller{ID: uuid.NewString()}, uuid.NewString()
			result, err := s.AcceptOrder(f.ctx, caller, o.ID, key)
			answers <- answer{caller, key, result, err}
		})
	}
	close(start)
	wg.Wait()
	close(answers)
	wins, rejects := 0, 0
	var winner, loser answer
	for a := range answers {
		if a.err != nil {
			t.Fatal(a.err)
		}
		switch a.result.Phase {
		case AssignmentSucceeded:
			wins++
			winner = a
		case AssignmentRejected:
			rejects++
			loser = a
		default:
			t.Fatalf("unexpected unresolved result: %+v", a.result)
		}
	}
	if wins != 1 || rejects != 49 {
		t.Fatalf("wins=%d rejects=%d", wins, rejects)
	}
	var attempts, assignments int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_assignment_attempts WHERE order_id=$1", o.ID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_audit WHERE order_id=$1 AND action='assign'", o.ID).Scan(&assignments); err != nil {
		t.Fatal(err)
	}
	if attempts != 50 || assignments != 1 {
		t.Fatalf("attempts=%d assignments=%d", attempts, assignments)
	}
	// Explicitly simulate removal at the existing aggregate seam. The production
	// B3 withdrawal compensation/history integration is a separate handoff.
	if err := credit.Revert(f.ctx, o.ID, winner.result.AttemptID); err != nil {
		t.Fatal(err)
	}
	f.mustTransition(t, o.ID, Withdraw, Actor{Kind: User, ID: winner.caller.ID})
	for _, a := range []answer{winner, loser} {
		replay, err := s.AcceptOrder(f.ctx, a.caller, o.ID, a.key)
		if err != nil || replay.Phase != a.result.Phase || replay.AttemptID != a.result.AttemptID || replay.Order.CourierID != nil {
			t.Fatalf("replay reassigned or lost outcome: %+v %v", replay, err)
		}
	}
}

// Source: C1.2.2 and the user's example: balance70, holds20+20, available30.
// Only one request can increase its hold from20 to50; the other offer stays Open.
func TestPostgresCounterofferBalanceAndRetry(t *testing.T) {
	f := setup(t)
	a, b := assignmentOpenOrder(t, f), assignmentOpenOrder(t, f)
	// Fixture's ordinary reward is15. Set the explicit user-provided20-credit
	// example before proposals, using SQL only for arranging these seed Orders.
	for _, o := range []*Order{&a, &b} {
		if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET reward_credits=20,reserved_credits=20 WHERE id=$1", o.ID); err != nil {
			t.Fatal(err)
		}
		o.RewardCredits, o.ReservedCredits = 20, 20
	}
	// Both seeded reservations belong to the same scripted requester account.
	b.RequesterID = a.RequesterID
	if _, err := f.pool.Exec(f.ctx, "UPDATE orders SET requester_id=$2 WHERE id=$1", b.ID, b.RequesterID); err != nil {
		t.Fatal(err)
	}
	clock, now := assignmentClock(b)
	credit := newAssignmentCredit(30, a, b)
	s := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	courier := Caller{ID: uuid.NewString()}
	fa, err := s.MakeOffer(f.ctx, courier, a.ID, 50, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	fb, err := s.MakeOffer(f.ctx, courier, b.ID, 50, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.AcceptOffer(f.ctx, Caller{ID: a.RequesterID}, fa.ID)
	if err != nil || first.Phase != AssignmentSucceeded {
		t.Fatalf("first: %+v %v", first, err)
	}
	credit.denyRevert = true
	second, err := s.AcceptOffer(f.ctx, Caller{ID: b.RequesterID}, fb.ID)
	if err != nil || second.Phase != RevertPending || second.Rejection != "insufficient_credits" || second.Offer.Status != OfferAccepting {
		t.Fatalf("unfenced refusal reopened offer: %+v %v", second, err)
	}
	credit.denyRevert = false
	clock.Add(int64(30 * time.Second))
	if err := s.RecoverAssignmentsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	var phase string
	if err := f.pool.QueryRow(f.ctx, "SELECT phase FROM order_assignment_attempts WHERE id=$1", second.AttemptID).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	offers, err := f.repo.Offers(f.ctx, Caller{ID: b.RequesterID}, b.ID, "")
	if err != nil || phase != string(AssignmentRejected) || len(offers) != 1 || offers[0].Status != OfferOpen {
		t.Fatalf("acknowledged refusal failed to reopen: phase=%s offers=%+v err=%v", phase, offers, err)
	}
	credit.mu.Lock()
	if credit.available != 0 || credit.views[a.ID].ReservedAmount != 50 || credit.views[b.ID].ReservedAmount != 20 {
		t.Fatal("scripted balance scenario mismatched")
	}
	credit.available = 30 // requester obtains credits before retrying the same proposal
	credit.mu.Unlock()
	clock.Add(int64(time.Second))
	retry, err := s.AcceptOffer(f.ctx, Caller{ID: b.RequesterID}, fb.ID)
	if err != nil || retry.Phase != AssignmentSucceeded || retry.AttemptID == second.AttemptID {
		t.Fatalf("retry: %+v %v", retry, err)
	}
}

// Source: R1.2.4, durable intent/recovery and strict final-assignment deadline.
func TestPostgresAssignmentRecoveryAndExpiry(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	clock, now := assignmentClock(o)
	credit := newAssignmentCredit(70, o)
	credit.denyReads = true
	s := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	caller, key := Caller{ID: uuid.NewString()}, uuid.NewString()
	pending, err := s.AcceptOrder(f.ctx, caller, o.ID, key)
	if err != nil || pending.Phase != AdjustmentPending {
		t.Fatalf("pending: %+v %v", pending, err)
	}
	// The remote adjustment succeeded, but its observation was unavailable.
	// A new application instance resolves the durable attempt after expiry.
	clock.Store(o.ExpiresAt.Add(time.Second).UnixNano())
	credit.mu.Lock()
	credit.denyReads = false
	credit.mu.Unlock()
	restarted := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	if err := restarted.RecoverAssignmentsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.AcceptOrder(f.ctx, caller, o.ID, key)
	if err != nil || result.Phase != AssignmentRejected || result.Order.CourierID != nil || result.Offer.Status != OfferVoid {
		t.Fatalf("late assignment: %+v %v", result, err)
	}
	credit.mu.Lock()
	defer credit.mu.Unlock()
	if credit.views[o.ID].State != "held" || credit.views[o.ID].ReservedAmount != o.ReservedCredits || credit.reverts != 1 {
		t.Fatalf("compensation: %+v reverts=%d", credit.views[o.ID], credit.reverts)
	}
}

// Source: the approved frozen Accepting offer and cancellation race policy.
func TestPostgresPendingOfferCannotBeReplacedAndCancellationWins(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	credit := newAssignmentCredit(70, o)
	s := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	courier := Caller{ID: uuid.NewString()}
	proposal, err := s.MakeOffer(f.ctx, courier, o.ID, 50, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	credit.afterAdjust = func() { close(entered); <-release }
	finished := make(chan error, 1)
	go func() { _, err := s.AcceptOffer(f.ctx, Caller{ID: o.RequesterID}, proposal.ID); finished <- err }()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("adjustment never started")
	}
	_, replaceErr := s.MakeOffer(f.ctx, courier, o.ID, 60, uuid.NewString())
	cancelErr := f.transition(o.ID, Cancel, Actor{Kind: User, ID: o.RequesterID})
	close(release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(replaceErr, ErrConflict) || cancelErr != nil {
		t.Fatalf("replace=%v cancel=%v", replaceErr, cancelErr)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.Status != Cancelled || current.CourierID != nil {
		t.Fatalf("cancellation lost: %+v %v", current, err)
	}
}

// Fresh private IDs must not disclose existence or create retained retry rows.
func TestPostgresAcceptanceAdmissionPrivacyAndBound(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	clock, now := assignmentClock(o)
	credit := newAssignmentCredit(70, o)
	credit.denyReads = true
	s := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	winner := Caller{ID: uuid.NewString()}
	first, err := s.AcceptOrder(f.ctx, winner, o.ID, uuid.NewString())
	if err != nil || first.Phase != AdjustmentPending {
		t.Fatalf("pending admission: %+v %v", first, err)
	}
	caller := Caller{ID: uuid.NewString()}
	key := uuid.NewString()
	rejected, err := s.AcceptOrder(f.ctx, caller, o.ID, key)
	if err != nil || rejected.Phase != AssignmentRejected {
		t.Fatalf("contending admission: %+v %v", rejected, err)
	}
	for range 10 {
		if _, err := s.AcceptOrder(f.ctx, caller, o.ID, uuid.NewString()); !errors.Is(err, ErrPrecondition) {
			t.Fatalf("fresh contention key: %v", err)
		}
	}
	var count int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_assignment_attempts WHERE order_id=$1", o.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retained contention rows=%d err=%v", count, err)
	}
	credit.denyReads = false
	clock.Add(int64(time.Minute))
	if err := s.ProcessAssignment(f.ctx, first.AttemptID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{o.ID, uuid.NewString()} {
		for range 5 {
			if _, err := s.AcceptOrder(f.ctx, Caller{ID: uuid.NewString()}, id, uuid.NewString()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("private/missing admission: %v", err)
			}
		}
	}
	replay, err := s.AcceptOrder(f.ctx, caller, o.ID, key)
	if err != nil || replay.AttemptID != rejected.AttemptID {
		t.Fatalf("retained rejection on private order: %+v %v", replay, err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_assignment_attempts WHERE order_id=$1", o.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("private-ID rows=%d err=%v", count, err)
	}
}
