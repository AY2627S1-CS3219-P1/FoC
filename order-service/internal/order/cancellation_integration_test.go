//go:build integration

package order

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func cancellationState(t *testing.T, f *fixture, status Status, courier string) Order {
	t.Helper()
	o := f.create(t)
	if status != PendingReservation && status != Rejected {
		f.mustTransition(t, o.ID, ConfirmReservation, Actor{Kind: System})
	}
	if status == Rejected {
		f.mustTransition(t, o.ID, RefuseReservation, Actor{Kind: System})
	}
	if status == Expired {
		f.mustTransition(t, o.ID, Expire, Actor{Kind: System})
	}
	if status == AwaitingPickup || status == Delivering || status == Delivered || status == Completed || status == Failed {
		f.mustTransition(t, o.ID, Assign, Actor{Kind: User, ID: courier})
	}
	if status == Delivering || status == Delivered || status == Completed || status == Failed {
		f.mustTransition(t, o.ID, Pickup, Actor{Kind: User, ID: courier})
	}
	if status == Failed {
		f.mustTransition(t, o.ID, Cancel, Actor{Kind: User, ID: courier})
	}
	if status == Delivered || status == Completed {
		f.mustTransition(t, o.ID, Deliver, Actor{Kind: User, ID: courier})
	}
	if status == Completed {
		f.mustTransition(t, o.ID, ConfirmReceipt, Actor{Kind: User, ID: o.RequesterID})
	}
	if status == Cancelled {
		f.mustTransition(t, o.ID, Cancel, Actor{Kind: User, ID: o.RequesterID})
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	return current
}

func TestPostgresCancellationParticipantsAndStates(t *testing.T) {
	f := setup(t)
	for _, status := range []Status{PendingReservation, AwaitingCourier, AwaitingPickup, Delivering, Delivered, Completed, Cancelled, Expired, Failed, Rejected} {
		for _, role := range []string{"requester", "courier", "foreign", "admin"} {
			for _, suspended := range []bool{false, true} {
				t.Run(string(status)+"/"+role+map[bool]string{true: "/suspended", false: "/active"}[suspended], func(t *testing.T) {
					courier := uuid.NewString()
					o := cancellationState(t, f, status, courier)
					caller := Caller{ID: uuid.NewString(), Suspended: suspended, Admin: role == "admin"}
					if role == "requester" {
						caller.ID = o.RequesterID
					}
					if role == "courier" {
						caller.ID = courier
					}
					now := func() time.Time { return o.UpdatedAt.Add(time.Second) }
					beforeAudit, beforeFacts, _ := f.counts(t, o.ID)
					result, err := NewCanceller(f.repo, nil, AssignmentOptions{Now: now}).Cancel(f.ctx, caller, o.ID, "  change of plans  ", uuid.NewString())
					allowed := role == "requester" && (status == AwaitingCourier || status == AwaitingPickup || status == Delivering) || role == "courier" && status == Delivering
					if !allowed {
						if err == nil {
							t.Fatalf("unexpected cancellation: %+v", result)
						}
						audit, facts, _ := f.counts(t, o.ID)
						if audit != beforeAudit || facts != beforeFacts {
							t.Fatal("rejection wrote audit/events")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					want, added := Cancelled, 2
					if role == "courier" {
						want, added = Failed, 3
					}
					if role == "courier" && status == AwaitingPickup {
						want, added = AwaitingPickup, 0
						if result.Status != CancellationPending || result.Order.PendingCancellationID == nil {
							t.Fatal("missing pending intent")
						}
						if err := f.transition(o.ID, Pickup, Actor{Kind: User, ID: courier}); !errors.Is(err, ErrPrecondition) {
							t.Fatalf("pending pickup: %v", err)
						}
					} else if result.Status != CancellationSucceeded || result.Order.CloseReason == nil || *result.Order.CloseReason != "change of plans" {
						t.Fatal("missing terminal result/reason")
					}
					if result.Order.Status != want || result.Order.ReservedCredits != o.ReservedCredits {
						t.Fatalf("result: %+v", result)
					}
					if (o.PickedUpAt == nil) != (result.Order.PickedUpAt == nil) || (o.CourierID == nil) != (result.Order.CourierID == nil) {
						t.Fatal("lost terminal observations")
					}
					audit, facts, _ := f.counts(t, o.ID)
					if audit != beforeAudit+1 || facts != beforeFacts+added {
						t.Fatalf("audit/facts: %d/%d", audit-beforeAudit, facts-beforeFacts)
					}
				})
			}
		}
	}
}

func TestPostgresWithdrawalRestorationAndLifetimeReplay(t *testing.T) {
	f := setup(t)
	for _, raised := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "counteroffer"}[raised], func(t *testing.T) {
			o := assignmentOpenOrder(t, f)
			clock, now := assignmentClock(o)
			credit := newAssignmentCredit(100, o)
			assigner := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
			courier := Caller{ID: uuid.NewString()}
			acceptKey := uuid.NewString()
			var assigned AssignmentResult
			var err error
			if raised {
				offer, e := assigner.MakeOffer(f.ctx, courier, o.ID, o.RewardCredits+20, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				assigned, err = assigner.AcceptOffer(f.ctx, Caller{ID: o.RequesterID}, offer.ID)
			} else {
				assigned, err = assigner.AcceptOrder(f.ctx, courier, o.ID, acceptKey)
			}
			if err != nil || assigned.Phase != AssignmentSucceeded {
				t.Fatalf("assign: %+v %v", assigned, err)
			}
			credit.denyRevert = true
			canceller := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
			key := uuid.NewString()
			first, err := canceller.Cancel(f.ctx, courier, o.ID, "  withdraw  ", key)
			if err != nil || first.Status != CancellationPending {
				t.Fatalf("withdraw: %+v %v", first, err)
			}
			// Equivalent concurrent requests, both keyed and keyless, join the same operation.
			var wg sync.WaitGroup
			answers := make(chan CancellationResult, 16)
			errs := make(chan error, 16)
			for i := range 16 {
				wg.Go(func() {
					retryKey := ""
					if i%2 == 0 {
						retryKey = uuid.NewString()
					}
					result, err := canceller.Cancel(f.ctx, courier, o.ID, "withdraw", retryKey)
					answers <- result
					errs <- err
				})
			}
			wg.Wait()
			close(answers)
			close(errs)
			for err := range errs {
				if err != nil {
					t.Fatal(err)
				}
			}
			for result := range answers {
				if result.OperationID != first.OperationID {
					t.Fatal("duplicate withdrawal")
				}
			}
			if _, err := canceller.Cancel(f.ctx, courier, o.ID, "different", key); !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("changed key: %v", err)
			}
			if _, err := canceller.Cancel(f.ctx, courier, uuid.NewString(), "withdraw", key); !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("changed missing order: %v", err)
			}
			other := assignmentOpenOrder(t, f)
			if _, err := canceller.Cancel(f.ctx, courier, other.ID, "withdraw", key); !errors.Is(err, ErrAlreadyExists) {
				t.Fatalf("changed order: %v", err)
			}
			// Simulate a restart; retry identity and original amount come only from PostgreSQL.
			credit.denyRevert = false
			clock.Add(int64(time.Minute))
			restarted := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
			if err := restarted.RecoverCancellationsOnce(f.ctx); err != nil {
				t.Fatal(err)
			}
			replay, err := restarted.Cancel(f.ctx, Caller{ID: courier.ID, Suspended: true}, o.ID, "withdraw", key)
			if err != nil || replay.Status != CancellationSucceeded || replay.OperationID != first.OperationID {
				t.Fatalf("recovered: %+v %v", replay, err)
			}
			if replay.Order.Status != AwaitingCourier || replay.Order.CourierID != nil || replay.Order.AssignedAt != nil || replay.Order.PendingCancellationID != nil || replay.Order.RewardCredits != o.RewardCredits || replay.Order.ReservedCredits != o.ReservedCredits || !replay.Order.ExpiresAt.Equal(o.ExpiresAt) {
				t.Fatalf("restoration: %+v", replay.Order)
			}
			fresh, err := assigner.AcceptOrder(f.ctx, courier, o.ID, uuid.NewString())
			if err != nil || fresh.Phase != AssignmentSucceeded || fresh.AttemptID == assigned.AttemptID {
				t.Fatalf("reaccept: %+v %v", fresh, err)
			}
			if !raised {
				old, err := assigner.AcceptOrder(f.ctx, courier, o.ID, acceptKey)
				if err != nil || old.AttemptID != assigned.AttemptID {
					t.Fatalf("old acceptance: %+v %v", old, err)
				}
			} else {
				old, err := assigner.AcceptOffer(f.ctx, Caller{ID: o.RequesterID}, assigned.Offer.ID)
				if err != nil || old.AttemptID != assigned.AttemptID {
					t.Fatalf("old offer: %+v %v", old, err)
				}
			}
			old, err := restarted.Cancel(f.ctx, courier, o.ID, "withdraw", key)
			if err != nil || old.Order.Status != AwaitingPickup || old.Status != CancellationSucceeded {
				t.Fatalf("old cancellation changed assignment: %+v %v", old, err)
			}
			// The former courier cannot cancel an assignment held by someone else.
			if _, err := restarted.Cancel(f.ctx, courier, o.ID, "new intent", uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			replacement := Caller{ID: uuid.NewString()}
			if _, err := assigner.AcceptOrder(f.ctx, replacement, o.ID, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.Cancel(f.ctx, courier, o.ID, "foreign", uuid.NewString()); !errors.Is(err, ErrForbidden) {
				t.Fatalf("former courier: %v", err)
			}
			var episodes, closed int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*), count(ended_at) FROM order_assignment_history WHERE order_id=$1", o.ID).Scan(&episodes, &closed); err != nil || episodes != 3 || closed != 2 {
				t.Fatalf("history %d/%d %v", episodes, closed, err)
			}
		})
	}
}

type delayedWithdrawalCredit struct {
	*assignmentCreditFixture
	entered chan struct{}
	finish  chan struct{}
}

func (c *delayedWithdrawalCredit) Revert(ctx context.Context, orderID, attemptID string) error {
	close(c.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.finish:
	}
	return c.assignmentCreditFixture.Revert(ctx, orderID, attemptID)
}

func TestPostgresCancellationSupersedesLateWithdrawal(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	credit := &delayedWithdrawalCredit{assignmentCreditFixture: newAssignmentCredit(100, o), entered: make(chan struct{}), finish: make(chan struct{})}
	courier := Caller{ID: uuid.NewString()}
	if _, err := NewAssigner(f.repo, credit, AssignmentOptions{Now: now}).AcceptOrder(f.ctx, courier, o.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	s := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
	key := uuid.NewString()
	done := make(chan error, 1)
	go func() { _, err := s.Cancel(f.ctx, courier, o.ID, "withdraw", key); done <- err }()
	<-credit.entered
	terminal, err := s.Cancel(f.ctx, Caller{ID: o.RequesterID, Suspended: true}, o.ID, "stop", uuid.NewString())
	if err != nil || terminal.Order.Status != Cancelled {
		t.Fatalf("terminal %+v %v", terminal, err)
	}
	close(credit.finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	replay, err := s.Cancel(f.ctx, courier, o.ID, "withdraw", key)
	if err != nil || replay.Status != CancellationSuperseded || replay.Order.Status != Cancelled || replay.Order.CourierID == nil {
		t.Fatalf("late reopen %+v %v", replay, err)
	}
	var outcomes int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).Scan(&outcomes); err != nil || outcomes != 1 {
		t.Fatalf("outcomes %d %v", outcomes, err)
	}
}

func TestPostgresCancellationFencesLateAcceptance(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	clock, now := assignmentClock(o)
	credit := newAssignmentCredit(100, o)
	entered, finish := make(chan struct{}), make(chan struct{})
	credit.afterAdjust = func() { close(entered); <-finish }
	s := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	done := make(chan error, 1)
	go func() {
		_, err := s.AcceptOrder(f.ctx, Caller{ID: uuid.NewString()}, o.ID, uuid.NewString())
		done <- err
	}()
	<-entered
	result, err := NewCanceller(f.repo, credit, AssignmentOptions{Now: now}).Cancel(f.ctx, Caller{ID: o.RequesterID}, o.ID, "stop", uuid.NewString())
	if err != nil || result.Order.Status != Cancelled {
		t.Fatalf("cancel %+v %v", result, err)
	}
	close(finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	credit.afterAdjust = nil
	clock.Add(int64(time.Minute))
	if err := s.RecoverAssignmentsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.Status != Cancelled || current.CourierID != nil {
		t.Fatalf("late assignment %+v %v", current, err)
	}
	var pending bool
	if err := f.pool.QueryRow(f.ctx, "SELECT EXISTS(SELECT 1 FROM order_assignment_attempts WHERE order_id=$1 AND phase IN ('adjustment_pending','revert_pending'))", o.ID).Scan(&pending); err != nil || pending {
		t.Fatalf("compensation pending %v %v", pending, err)
	}
}

func TestPostgresCancellationRacesAndAtomicity(t *testing.T) {
	f := setup(t)
	for _, action := range []Action{Pickup, Deliver} {
		for range 8 {
			status := AwaitingPickup
			if action == Deliver {
				status = Delivering
			}
			courier := uuid.NewString()
			o := cancellationState(t, f, status, courier)
			_, now := assignmentClock(o)
			s := NewCanceller(f.repo, nil, AssignmentOptions{Now: now})
			start := make(chan struct{})
			done := make(chan error, 2)
			go func() {
				<-start
				_, err := s.Cancel(f.ctx, Caller{ID: o.RequesterID}, o.ID, "race", uuid.NewString())
				done <- err
			}()
			go func() { <-start; done <- f.transition(o.ID, action, Actor{Kind: User, ID: courier}) }()
			close(start)
			for range 2 {
				if err := <-done; err != nil && !errors.Is(err, ErrPrecondition) {
					t.Fatal(err)
				}
			}
			current, err := f.repo.Get(f.ctx, o.ID)
			if err != nil {
				t.Fatal(err)
			}
			if current.Status != Cancelled && !(action == Deliver && current.Status == Delivered) {
				t.Fatalf("invalid lifecycle: %+v", current)
			}
			var outcomes int
			if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE order_id=$1 AND kind='order.outcome'", o.ID).Scan(&outcomes); err != nil {
				t.Fatal(err)
			}
			if outcomes != map[bool]int{true: 1, false: 0}[current.Status == Cancelled] {
				t.Fatal("duplicate/missing outcome")
			}
		}
	}
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	s := NewCanceller(f.repo, nil, AssignmentOptions{Now: now})
	key := uuid.NewString()
	if _, err := f.pool.Exec(f.ctx, `CREATE FUNCTION fail_cancellation_fact() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'scripted outbox failure'; END $$; CREATE TRIGGER fail_cancellation_fact BEFORE INSERT ON order_outbox FOR EACH ROW EXECUTE FUNCTION fail_cancellation_fact()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(f.ctx, Caller{ID: o.RequesterID}, o.ID, "atomic", key); err == nil {
		t.Fatal("outbox failure did not abort")
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.Revision != o.Revision || current.Status != AwaitingCourier {
		t.Fatalf("partial aggregate %+v %v", current, err)
	}
	var operations, keys int
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_cancellations WHERE order_id=$1", o.ID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_idempotency_keys WHERE idempotency_key=$1", key).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if operations != 0 || keys != 0 {
		t.Fatal("partial operation/key")
	}
	if _, err := f.pool.Exec(f.ctx, "DROP TRIGGER fail_cancellation_fact ON order_outbox; DROP FUNCTION fail_cancellation_fact()"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	answers := make(chan CancellationResult, 16)
	errs := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			r, e := s.Cancel(f.ctx, Caller{ID: o.RequesterID}, o.ID, "atomic", key)
			answers <- r
			errs <- e
		})
	}
	wg.Wait()
	close(answers)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id string
	for result := range answers {
		if id == "" {
			id = result.OperationID
		}
		if result.OperationID != id || result.Order.Status != Cancelled {
			t.Fatal("duplicate terminal cancellation")
		}
	}
}

func TestPostgresWithdrawalRequiresAcknowledgedMatchingHeld(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	clock, now := assignmentClock(o)
	credit := newAssignmentCredit(100, o)
	courier := Caller{ID: uuid.NewString()}
	if _, err := NewAssigner(f.repo, credit, AssignmentOptions{Now: now}).AcceptOrder(f.ctx, courier, o.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	credit.denyReads = true
	s := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
	key := uuid.NewString()
	result, err := s.Cancel(f.ctx, courier, o.ID, "", key)
	if err != nil || result.Status != CancellationPending {
		t.Fatalf("lost observation %+v %v", result, err)
	}
	credit.denyReads = false
	credit.views[o.ID] = AssignmentReservation{OrderID: o.ID, RequesterID: o.RequesterID, State: "released", OriginalAmount: o.RewardCredits}
	clock.Add(int64(time.Minute))
	if err := s.RecoverCancellationsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	result, err = s.Cancel(f.ctx, courier, o.ID, "", key)
	if err != nil || result.Status != CancellationPending {
		t.Fatalf("released reopened %+v %v", result, err)
	}
	credit.views[o.ID] = AssignmentReservation{OrderID: o.ID, RequesterID: o.RequesterID, State: "held", OriginalAmount: o.RewardCredits, ReservedAmount: o.RewardCredits}
	credit.denyRevert = true
	clock.Add(int64(time.Minute))
	if err := s.RecoverCancellationsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	result, err = s.Cancel(f.ctx, courier, o.ID, "", key)
	if err != nil || result.Status != CancellationPending {
		t.Fatalf("unacknowledged reopened %+v %v", result, err)
	}
	credit.denyRevert = false
	clock.Add(int64(time.Minute))
	if err := s.RecoverCancellationsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	result, err = s.Cancel(f.ctx, courier, o.ID, "", key)
	if err != nil || result.Status != CancellationSucceeded {
		t.Fatalf("matching Held failed %+v %v", result, err)
	}
	// A legacy assignment without durable Credit identity never guesses a reversal key.
	legacy := cancellationState(t, f, AwaitingPickup, courier.ID)
	missing, err := s.Cancel(f.ctx, courier, legacy.ID, "legacy", uuid.NewString())
	if err != nil || missing.Status != CancellationPending {
		t.Fatalf("missing identity %+v %v", missing, err)
	}
}

type firstDelayedWithdrawalCredit struct {
	*assignmentCreditFixture
	calls           int
	muCalls         sync.Mutex
	entered, finish chan struct{}
}

func (c *firstDelayedWithdrawalCredit) Revert(ctx context.Context, orderID, attemptID string) error {
	c.muCalls.Lock()
	c.calls++
	first := c.calls == 1
	c.muCalls.Unlock()
	if first {
		close(c.entered)
		<-c.finish
	}
	return c.assignmentCreditFixture.Revert(ctx, orderID, attemptID)
}

func TestPostgresWithdrawalLeaseFencesLaterAssignment(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	clock, now := assignmentClock(o)
	credit := &firstDelayedWithdrawalCredit{assignmentCreditFixture: newAssignmentCredit(100, o), entered: make(chan struct{}), finish: make(chan struct{})}
	courier := Caller{ID: uuid.NewString()}
	assigner := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	if _, err := assigner.AcceptOrder(f.ctx, courier, o.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	s := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
	key := uuid.NewString()
	done := make(chan error, 1)
	go func() { _, err := s.Cancel(f.ctx, courier, o.ID, "withdraw", key); done <- err }()
	<-credit.entered
	clock.Add(int64(time.Minute))
	// A new worker claims the expired lease and completes the same reversal.
	if err := NewCanceller(f.repo, credit, AssignmentOptions{Now: now}).RecoverCancellationsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	later, err := assigner.AcceptOrder(f.ctx, courier, o.ID, uuid.NewString())
	if err != nil || later.Phase != AssignmentSucceeded {
		t.Fatalf("later acceptance %+v %v", later, err)
	}
	close(credit.finish)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	current, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || current.Status != AwaitingPickup || !current.IsAssignedTo(courier.ID) || current.PendingCancellationID != nil {
		t.Fatalf("late generation changed later assignment %+v %v", current, err)
	}
	credit.mu.Lock()
	view := credit.views[o.ID]
	credit.mu.Unlock()
	if view.OfferID != later.AttemptID || view.State != "assigned" {
		t.Fatalf("late reversal changed financial identity %+v", view)
	}
}
