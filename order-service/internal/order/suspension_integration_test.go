//go:build integration

package order

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestPostgresSuspensionRemovalTargetsAssignmentAndReusesRecovery(t *testing.T) {
	f := setup(t)
	o := assignmentOpenOrder(t, f)
	clock, now := assignmentClock(o)
	credit := newAssignmentCredit(100, o)
	assigner := NewAssigner(f.repo, credit, AssignmentOptions{Now: now})
	courier := Caller{ID: uuid.NewString()}
	offer, err := assigner.MakeOffer(f.ctx, courier, o.ID, o.RewardCredits+20, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := assigner.AcceptOffer(f.ctx, Caller{ID: o.RequesterID}, offer.ID)
	if err != nil || assigned.Phase != AssignmentSucceeded {
		t.Fatalf("assignment: %+v %v", assigned, err)
	}
	target := SuspensionRemoval{DecisionID: uuid.NewString(), OrderID: o.ID, CourierID: courier.ID, AssignmentID: assigned.AttemptID}
	canceller := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
	wrongAssignment := target
	wrongAssignment.AssignmentID = uuid.NewString()
	if result, err := canceller.RemoveSuspendedAssignment(f.ctx, wrongAssignment); err != nil || result != nil {
		t.Fatalf("unrelated assignment: %+v %v", result, err)
	}
	if result, err := NewCanceller(f.repo, nil, AssignmentOptions{Now: now}).RemoveSuspendedAssignment(f.ctx, target); !errors.Is(err, ErrPrecondition) || result != nil {
		t.Fatalf("unconfigured recovery: %+v %v", result, err)
	}
	first, err := canceller.RemoveSuspendedAssignment(f.ctx, target)
	if err != nil || first == nil || first.Status != CancellationPending || first.Order.PendingCancellationID == nil {
		t.Fatalf("admission: %+v %v", first, err)
	}
	if credit.reverts != 0 || first.Order.RewardCredits != o.RewardCredits+20 {
		t.Fatal("admission called Credit or restored reward before recovery")
	}
	audit, facts, _ := f.counts(t, o.ID)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			repeat, err := canceller.RemoveSuspendedAssignment(f.ctx, target)
			if err != nil || repeat == nil || repeat.OperationID != first.OperationID {
				t.Errorf("duplicate admission: %+v %v", repeat, err)
			}
		})
	}
	wg.Wait()
	if a, n, _ := f.counts(t, o.ID); a != audit || n != facts || credit.reverts != 0 {
		t.Fatal("duplicate instruction repeated audit, events or credit work")
	}
	progress := NewProgress(NewMutator(f.repo))
	if _, err := progress.ConfirmPickup(f.ctx, courier, o.ID); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("pickup during recovery: %v", err)
	}
	// Domain admission may return a retained rejection with nil error. Only a
	// successful/pending financial attempt would accept a replacement courier.
	adjusts := credit.adjusts
	competing, err := assigner.AcceptOrder(f.ctx, Caller{ID: uuid.NewString()}, o.ID, uuid.NewString())
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrPrecondition) && !errors.Is(err, ErrConflict) {
		t.Fatalf("unexpected competing acceptance failure: %v", err)
	}
	if err == nil && (competing.Phase != AssignmentRejected || competing.Order.PendingCancellationID == nil || !competing.Order.IsAssignedTo(courier.ID)) {
		t.Fatalf("accepted during recovery: %+v", competing)
	}
	if credit.adjusts != adjusts {
		t.Fatal("competing acceptance called Credit during removal recovery")
	}
	credit.denyRevert = true
	if err := canceller.ProcessCancellation(f.ctx, first.OperationID); err != nil {
		t.Fatal(err)
	}
	stillPending, _ := f.repo.Get(f.ctx, o.ID)
	if stillPending.Status != AwaitingPickup || stillPending.PendingCancellationID == nil {
		t.Fatal("uncertain reversal reopened the order")
	}
	credit.denyRevert = false
	clock.Add(int64(time.Minute))
	restarted := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
	if err := restarted.RecoverCancellationsOnce(f.ctx); err != nil {
		t.Fatal(err)
	}
	reopened, err := f.repo.Get(f.ctx, o.ID)
	if err != nil || reopened.Status != AwaitingCourier || reopened.CourierID != nil || reopened.PendingCancellationID != nil || reopened.RewardCredits != o.RewardCredits || reopened.ReservedCredits != o.ReservedCredits || !reopened.ExpiresAt.Equal(o.ExpiresAt) {
		t.Fatalf("restoration: %+v %v", reopened, err)
	}
	var kind, action string
	if err := f.pool.QueryRow(f.ctx, "SELECT actor_kind,action FROM order_audit WHERE order_id=$1 ORDER BY revision DESC LIMIT 1", o.ID).Scan(&kind, &action); err != nil || kind != "system" || action != string(RemoveSuspendedCourier) {
		t.Fatalf("automatic-removal audit %s/%s: %v", kind, action, err)
	}
	replacement := Caller{ID: uuid.NewString()}
	if _, err := assigner.AcceptOrder(f.ctx, replacement, o.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	checkStale := func(wantCourier string) {
		t.Helper()
		before, _ := f.repo.Get(f.ctx, o.ID)
		calls := credit.reverts
		for range 2 {
			if repeat, err := restarted.RemoveSuspendedAssignment(f.ctx, target); err != nil || repeat != nil {
				t.Fatalf("stale assignment removal: %+v %v", repeat, err)
			}
			if err := restarted.ProcessCancellation(f.ctx, first.OperationID); err != nil {
				t.Fatal(err)
			}
		}
		after, _ := f.repo.Get(f.ctx, o.ID)
		if !after.IsAssignedTo(wantCourier) || after.Revision != before.Revision || credit.reverts != calls {
			t.Fatal("delayed A-removal changed replacement assignment or repeated Credit calls")
		}
	}
	checkStale(replacement.ID)
	if _, err := restarted.Cancel(f.ctx, replacement, o.ID, "withdraw", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	if _, err := assigner.AcceptOrder(f.ctx, courier, o.ID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	checkStale(courier.ID)
}

func TestPostgresPickupAndSuspensionRemovalShareParentLock(t *testing.T) {
	f := setup(t)
	for range 8 {
		o := assignmentOpenOrder(t, f)
		_, now := assignmentClock(o)
		credit := newAssignmentCredit(100, o)
		courier := Caller{ID: uuid.NewString()}
		assignment, err := NewAssigner(f.repo, credit, AssignmentOptions{Now: now}).AcceptOrder(f.ctx, courier, o.ID, uuid.NewString())
		if err != nil {
			t.Fatal(err)
		}
		target := SuspensionRemoval{DecisionID: uuid.NewString(), OrderID: o.ID, CourierID: courier.ID, AssignmentID: assignment.AttemptID}
		canceller := NewCanceller(f.repo, credit, AssignmentOptions{Now: now})
		progress := NewProgress(NewMutator(f.repo))
		var pickupErr, removalErr error
		var removal *CancellationResult
		var wg sync.WaitGroup
		wg.Go(func() { _, pickupErr = progress.ConfirmPickup(f.ctx, courier, o.ID) })
		wg.Go(func() { removal, removalErr = canceller.RemoveSuspendedAssignment(f.ctx, target) })
		wg.Wait()
		if removalErr != nil {
			t.Fatal(removalErr)
		}
		stored, err := f.repo.Get(f.ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if pickupErr == nil {
			if removal != nil || stored.Status != Delivering || stored.PendingCancellationID != nil || stored.PickedUpAt == nil {
				t.Fatal("pickup won but automatic removal changed its delivery")
			}
		} else if !errors.Is(pickupErr, ErrPrecondition) || removal == nil || stored.Status != AwaitingPickup || stored.PendingCancellationID == nil || stored.PickedUpAt != nil {
			t.Fatalf("removal won without pickup fence: %v %+v", pickupErr, stored)
		}
		if credit.reverts != 0 {
			t.Fatal("race admission called Credit")
		}
	}
	// Deterministically establish pickup first, independent of scheduler choices
	// in the concurrent cases above.
	o := assignmentOpenOrder(t, f)
	_, now := assignmentClock(o)
	credit := newAssignmentCredit(100, o)
	courier := Caller{ID: uuid.NewString()}
	assignment, err := NewAssigner(f.repo, credit, AssignmentOptions{Now: now}).AcceptOrder(f.ctx, courier, o.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	progress := NewProgress(NewMutator(f.repo))
	if _, err := progress.ConfirmPickup(f.ctx, courier, o.ID); err != nil {
		t.Fatal(err)
	}
	target := SuspensionRemoval{DecisionID: uuid.NewString(), OrderID: o.ID, CourierID: courier.ID, AssignmentID: assignment.AttemptID}
	if result, err := NewCanceller(f.repo, credit, AssignmentOptions{Now: now}).RemoveSuspendedAssignment(f.ctx, target); err != nil || result != nil || credit.reverts != 0 {
		t.Fatalf("after pickup: %+v %v, Credit calls %d", result, err, credit.reverts)
	}
}
