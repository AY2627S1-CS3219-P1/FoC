package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSuspensionRemovalRequiresSpecificDecisionAndAssignment(t *testing.T) {
	id := uuid.NewString()
	for _, target := range []SuspensionRemoval{
		{},
		{OrderID: id, CourierID: id, AssignmentID: id},
		{DecisionID: id, OrderID: id, CourierID: id},
		{DecisionID: id, OrderID: id, AssignmentID: id},
		{DecisionID: id, CourierID: id, AssignmentID: id},
		{DecisionID: id, OrderID: id, CourierID: id, AssignmentID: uuid.Nil.String()},
	} {
		if _, err := NewCanceller(nil, nil, AssignmentOptions{}).RemoveSuspendedAssignment(context.Background(), target); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid target %+v: %v", target, err)
		}
	}
}

// Embed the unused transaction methods so any unexpected dependency call fails
// the test instead of silently implementing unrelated assignment behavior.
type suspensionMemory struct {
	CancellationTx
	current    Order
	assignment AssignmentAttempt
	operations map[string]Cancellation
	changes    []Change
}

func (m *suspensionMemory) WithinCancellation(_ context.Context, fn func(CancellationTx) error) error {
	return fn(m)
}
func (m *suspensionMemory) LockOrder(context.Context, string) (Order, error) {
	return cloneOrder(m.current), nil
}
func (m *suspensionMemory) Relationships(context.Context, string) ([]Offer, error) { return nil, nil }
func (m *suspensionMemory) Apply(_ context.Context, c Change) error {
	if err := validateChange(&m.current, c); err != nil {
		return err
	}
	m.current = cloneOrder(c.Order)
	m.changes = append(m.changes, c)
	return nil
}
func (m *suspensionMemory) CurrentSuccessfulAssignment(context.Context, string) (*AssignmentAttempt, error) {
	a := m.assignment
	return &a, nil
}
func (m *suspensionMemory) FindCancellation(_ context.Context, id string) (Cancellation, error) {
	return m.operations[id], nil
}
func (m *suspensionMemory) InsertCancellation(_ context.Context, w Cancellation) error {
	m.operations[w.ID] = w
	return nil
}
func (m *suspensionMemory) SaveCancellation(_ context.Context, w Cancellation, expected int64) error {
	if m.operations[w.ID].Generation != expected {
		return ErrConflict
	}
	m.operations[w.ID] = w
	return nil
}
func (m *suspensionMemory) CancellationOrderID(_ context.Context, id string) (string, error) {
	return m.operations[id].OrderID, nil
}
func (*suspensionMemory) DueCancellations(context.Context, time.Time, int32) ([]string, error) {
	panic("unused")
}

type suspensionCredit struct {
	view  AssignmentReservation
	deny  bool
	calls int
}

func (c *suspensionCredit) Revert(context.Context, string, string) error {
	c.calls++
	if c.deny {
		return ErrUnavailable
	}
	c.view.State, c.view.ReservedAmount, c.view.OfferID, c.view.CourierID = "held", c.view.OriginalAmount, "", ""
	return nil
}
func (c *suspensionCredit) AssignmentReservation(context.Context, string) (AssignmentReservation, error) {
	return c.view, nil
}

func TestSuspensionRemovalRestoresOnlyItsAssignment(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	courier := uuid.NewString()
	o := Order{
		ID: uuid.NewString(), RequesterID: uuid.NewString(), CourierID: &courier,
		Status: AwaitingPickup, AssignedAt: &now, RewardCredits: 35, ReservedCredits: 35,
		ExpiresAt: now.Add(time.Hour), CreatedAt: now, UpdatedAt: now, Revision: 3,
	}
	assignment := AssignmentAttempt{ID: uuid.NewString(), OrderID: o.ID, CourierID: courier, Phase: AssignmentSucceeded, OriginalAmount: 15}
	m := &suspensionMemory{current: o, assignment: assignment, operations: map[string]Cancellation{}}
	if _, _, err := Transition(o, RemoveSuspendedCourier, Actor{Kind: System}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("automatic removal without recovery intent: %v", err)
	}
	credit := &suspensionCredit{deny: true, view: AssignmentReservation{
		OrderID: o.ID, RequesterID: o.RequesterID, OriginalAmount: 15, ReservedAmount: 35,
		State: "assigned", OfferID: assignment.ID, CourierID: courier,
	}}
	s := NewCanceller(m, credit, AssignmentOptions{Now: func() time.Time { return now }})
	target := SuspensionRemoval{DecisionID: uuid.NewString(), OrderID: o.ID, CourierID: courier, AssignmentID: assignment.ID}
	first, err := s.RemoveSuspendedAssignment(ctx, target)
	if err != nil || first == nil || first.Order.PendingCancellationID == nil || credit.calls != 0 {
		t.Fatalf("admission: %+v %v calls=%d", first, err, credit.calls)
	}
	for range 3 {
		repeat, err := s.RemoveSuspendedAssignment(ctx, target)
		if err != nil || repeat == nil || repeat.OperationID != first.OperationID || len(m.changes) != 1 || credit.calls != 0 {
			t.Fatalf("duplicate admission: %+v %v", repeat, err)
		}
	}
	if _, _, err := Transition(m.current, Pickup, Actor{Kind: User, ID: courier}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("pickup during recovery: %v", err)
	}
	if err := s.ProcessCancellation(ctx, first.OperationID); err != nil {
		t.Fatal(err)
	}
	if m.current.Status != AwaitingPickup || m.current.RewardCredits != 35 {
		t.Fatal("uncertain reversal reopened")
	}
	credit.deny = false
	now = now.Add(time.Minute)
	// A new service instance retains no admission state; recovery uses the store.
	restarted := NewCanceller(m, credit, AssignmentOptions{Now: func() time.Time { return now }})
	if err := restarted.ProcessCancellation(ctx, first.OperationID); err != nil {
		t.Fatal(err)
	}
	if m.current.Status != AwaitingCourier || m.current.CourierID != nil || m.current.PendingCancellationID != nil || m.current.RewardCredits != 15 || m.current.ReservedCredits != 15 || !m.current.ExpiresAt.Equal(o.ExpiresAt) {
		t.Fatalf("restored order: %+v", m.current)
	}
	if len(m.changes) != 2 || m.changes[0].Actor.Kind != System || m.changes[1].Action != RemoveSuspendedCourier {
		t.Fatal("automatic removal is not audited as system work")
	}
	calls := credit.calls
	for _, nextCourier := range []string{uuid.NewString(), courier} {
		m.current.Status, m.current.CourierID, m.current.AssignedAt = AwaitingPickup, &nextCourier, &now
		m.assignment.ID, m.assignment.CourierID = uuid.NewString(), nextCourier
		if result, err := restarted.RemoveSuspendedAssignment(ctx, target); err != nil || result != nil {
			t.Fatalf("stale instruction: %+v %v", result, err)
		}
		if err := restarted.ProcessCancellation(ctx, first.OperationID); err != nil {
			t.Fatal(err)
		}
		if !m.current.IsAssignedTo(nextCourier) || credit.calls != calls || len(m.changes) != 2 {
			t.Fatal("stale instruction changed replacement or repeated Credit")
		}
	}
}
