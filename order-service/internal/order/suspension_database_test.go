package order

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

func (c *suspensionCredit) Adjust(_ context.Context, in AdjustmentIntent) error {
	if c.view.OrderID != in.OrderID {
		return ErrConflict
	}
	c.view.State, c.view.OfferID, c.view.CourierID, c.view.ReservedAmount = "assigned", in.OfferID, in.CourierID, in.Amount
	return nil
}

// Runs against an explicitly configured throwaway database. This exercises the
// production repository and migrations directly; it does not need a container
// orchestration library and does not certify live Credit behavior.
func suspensionDatabase(t *testing.T) (context.Context, *pgxpool.Pool, *PostgresRepository, *goose.Provider) {
	t.Helper()
	dsn := os.Getenv("SUSPENSION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SUSPENSION_TEST_DATABASE_URL not set; requires disposable PostgreSQL 18")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	var version int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer").Scan(&version); err != nil || version < 180000 || version >= 190000 {
		t.Fatalf("requires disposable PostgreSQL 18: %d %v", version, err)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, pool, NewPostgresRepository(pool), provider
}

func TestSuspensionRemovalDatabase(t *testing.T) {
	ctx, _, repo, provider := suspensionDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := Order{
		ID: uuid.NewString(), RequesterID: uuid.NewString(), Status: PendingReservation,
		Pickup:          LocationSnapshot{LocationID: uuid.NewString(), Name: "Cafe", BuildingName: "COM2"},
		Delivery:        LocationSnapshot{LocationID: uuid.NewString(), Name: "Lobby", BuildingName: "PGP"},
		ItemDescription: "Lunch", RewardCredits: 15, ReservedCredits: 15,
		DeliveryDuration: 15 * time.Minute, ExpiresAt: now.Add(time.Hour), Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	creation, err := NewChange(nil, o, Actor{Kind: User, ID: o.RequesterID}, Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Within(ctx, func(tx Tx) error { return tx.Apply(ctx, creation) }); err != nil {
		t.Fatal(err)
	}
	mutator := NewMutator(repo)
	if err := mutator.Mutate(ctx, o.ID, func(current Order, _ []Offer) (*Change, error) {
		after := cloneOrder(current)
		after.Status, after.Revision = AwaitingCourier, current.Revision+1
		change, err := NewChange(&current, after, Actor{Kind: System}, ConfirmReservation, nil, uuid.NewString)
		return &change, err
	}); err != nil {
		t.Fatal(err)
	}
	credit := &suspensionCredit{view: AssignmentReservation{OrderID: o.ID, RequesterID: o.RequesterID, OriginalAmount: 15, ReservedAmount: 15, State: "held"}}
	assigner := NewAssigner(repo, credit, AssignmentOptions{Now: func() time.Time { return now }})
	courier := Caller{ID: uuid.NewString()}
	offer, err := assigner.MakeOffer(ctx, courier, o.ID, 35, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := assigner.AcceptOffer(ctx, Caller{ID: o.RequesterID}, offer.ID)
	if err != nil || assigned.Phase != AssignmentSucceeded {
		t.Fatalf("assign: %+v %v", assigned, err)
	}
	target := SuspensionRemoval{DecisionID: uuid.NewString(), OrderID: o.ID, CourierID: courier.ID, AssignmentID: assigned.AttemptID}
	s := NewCanceller(repo, credit, AssignmentOptions{Now: func() time.Time { return now }})
	first, err := s.RemoveSuspendedAssignment(ctx, target)
	if err != nil || first == nil {
		t.Fatalf("remove: %+v %v", first, err)
	}
	for range 3 {
		repeat, err := s.RemoveSuspendedAssignment(ctx, target)
		if err != nil || repeat == nil || repeat.OperationID != first.OperationID || credit.calls != 0 {
			t.Fatalf("repeat: %+v %v", repeat, err)
		}
	}
	if _, err := NewProgress(mutator).ConfirmPickup(ctx, courier, o.ID); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("pending pickup: %v", err)
	}
	if _, err := provider.DownTo(ctx, 6); err == nil {
		t.Fatal("downgrade discarded pending suspension identity")
	}
	// A provider rolls back one migration at a time; restore any newer unused
	// migration removed before the pending-removal guard rejected the downgrade.
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	credit.deny = true
	if err := s.ProcessCancellation(ctx, first.OperationID); err != nil {
		t.Fatal(err)
	}
	pending, err := repo.Get(ctx, o.ID)
	if err != nil || pending.Status != AwaitingPickup || pending.RewardCredits != 35 {
		t.Fatalf("uncertain recovery: %+v %v", pending, err)
	}
	credit.deny = false
	now = now.Add(time.Minute)
	restarted := NewCanceller(repo, credit, AssignmentOptions{Now: func() time.Time { return now }})
	if err := restarted.RecoverCancellationsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, o.ID)
	if err != nil || stored.Status != AwaitingCourier || stored.CourierID != nil || stored.PendingCancellationID != nil || stored.RewardCredits != 15 || stored.ReservedCredits != 15 || !stored.ExpiresAt.Equal(o.ExpiresAt) {
		t.Fatalf("restoration: %+v %v", stored, err)
	}
	for _, nextCourier := range []Caller{{ID: uuid.NewString()}, courier} {
		assignment, err := assigner.AcceptOrder(ctx, nextCourier, o.ID, uuid.NewString())
		if err != nil || assignment.Phase != AssignmentSucceeded {
			t.Fatalf("replacement: %+v %v", assignment, err)
		}
		calls := credit.calls
		if repeat, err := restarted.RemoveSuspendedAssignment(ctx, target); err != nil || repeat != nil {
			t.Fatalf("delayed removal: %+v %v", repeat, err)
		}
		if err := restarted.ProcessCancellation(ctx, first.OperationID); err != nil {
			t.Fatal(err)
		}
		after, err := repo.Get(ctx, o.ID)
		if err != nil || !after.IsAssignedTo(nextCourier.ID) || after.Revision != assignment.Order.Revision || credit.calls != calls {
			t.Fatalf("changed replacement: %+v %v", after, err)
		}
		if _, err := restarted.Cancel(ctx, nextCourier, o.ID, "withdraw", uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	// Pickup takes effect first: this instruction must preserve custody and must
	// not dispatch financial recovery, even though the courier is now suspended.
	assigned, err = assigner.AcceptOrder(ctx, courier, o.ID, uuid.NewString())
	if err != nil || assigned.Phase != AssignmentSucceeded {
		t.Fatalf("assignment before pickup: %+v %v", assigned, err)
	}
	progress := NewProgress(mutator)
	if _, err := progress.ConfirmPickup(ctx, courier, o.ID); err != nil {
		t.Fatal(err)
	}
	postPickup := SuspensionRemoval{DecisionID: uuid.NewString(), OrderID: o.ID, CourierID: courier.ID, AssignmentID: assigned.AttemptID}
	calls := credit.calls
	if result, err := restarted.RemoveSuspendedAssignment(ctx, postPickup); err != nil || result != nil || credit.calls != calls {
		t.Fatalf("removal after pickup: %+v %v", result, err)
	}
	if _, err := progress.ConfirmPickup(ctx, Caller{ID: courier.ID, Suspended: true}, o.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAccountSuspensionFenceDatabase(t *testing.T) {
	ctx, _, repo, provider := suspensionDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	courier := Caller{ID: uuid.NewString()}
	createOpen := func(requester string) Order {
		t.Helper()
		o := Order{
			ID: uuid.NewString(), RequesterID: requester, Status: PendingReservation,
			Pickup:          LocationSnapshot{LocationID: uuid.NewString(), Name: "Cafe", BuildingName: "COM2"},
			Delivery:        LocationSnapshot{LocationID: uuid.NewString(), Name: "Lobby", BuildingName: "PGP"},
			ItemDescription: "Lunch", RewardCredits: 15, ReservedCredits: 15, DeliveryDuration: 15 * time.Minute,
			ExpiresAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now,
		}
		c, err := NewChange(nil, o, Actor{Kind: User, ID: requester}, Create, nil, uuid.NewString)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Within(ctx, func(tx Tx) error { return tx.Apply(ctx, c) }); err != nil {
			t.Fatal(err)
		}
		if err := NewMutator(repo).Mutate(ctx, o.ID, func(before Order, _ []Offer) (*Change, error) {
			after := cloneOrder(before)
			after.Revision++
			after.Status = AwaitingCourier
			c, err := NewChange(&before, after, Actor{Kind: System}, ConfirmReservation, nil, uuid.NewString)
			return &c, err
		}); err != nil {
			t.Fatal(err)
		}
		return o
	}
	o := createOpen(uuid.NewString())
	other := createOpen(uuid.NewString())
	credit := &suspensionCredit{view: AssignmentReservation{OrderID: o.ID, RequesterID: o.RequesterID, OriginalAmount: 15, ReservedAmount: 15, State: "held"}}
	assigner := NewAssigner(repo, credit, AssignmentOptions{Now: func() time.Time { return now }})
	assigned, err := assigner.AcceptOrder(ctx, courier, o.ID, uuid.NewString())
	if err != nil || assigned.Phase != AssignmentSucceeded {
		t.Fatalf("assignment: %+v %v", assigned, err)
	}
	decision := AccountSuspensionDecision{ID: uuid.NewString(), UserID: courier.ID, AdminID: uuid.NewString(), Reason: "explicit admin decision", Revision: 1, Suspended: true}
	retainedOffer, err := assigner.MakeOffer(ctx, courier, other.ID, 25, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := NewAccountSuspender(repo, nil, AssignmentOptions{}).Apply(ctx, decision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("unconfigured recovery accepted account decision: %v", err)
	}
	coordinator := NewAccountSuspender(repo, credit, AssignmentOptions{Now: func() time.Time { return now }})
	if err := coordinator.Apply(ctx, decision); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, o.ID)
	if err != nil || stored.PendingCancellationID == nil || stored.Status != AwaitingPickup {
		t.Fatalf("automatic capture: %+v %v", stored, err)
	}
	if _, err := NewProgress(NewMutator(repo)).ConfirmPickup(ctx, courier, o.ID); err == nil {
		t.Fatal("stale preflight caller bypassed suspension")
	}
	// The caller deliberately has Suspended=false: local authority, not the
	// earlier User JWT/profile snapshot, must reject the new intent.
	if _, err := assigner.MakeOffer(ctx, courier, other.ID, 25, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale offer: %v", err)
	}
	if _, err := assigner.AcceptOrder(ctx, courier, other.ID, uuid.NewString()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale acceptance: %v", err)
	}
	if _, err := assigner.AcceptOffer(ctx, Caller{ID: other.RequesterID}, retainedOffer.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("requester accepted suspended courier's old offer: %v", err)
	}
	newRequest := cloneOrder(o)
	newRequest.ID, newRequest.RequesterID = uuid.NewString(), courier.ID
	creation, err := NewChange(nil, newRequest, Actor{Kind: User, ID: courier.ID}, Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Within(ctx, func(tx Tx) error { return tx.Apply(ctx, creation) }); !errors.Is(err, ErrForbidden) {
		t.Fatalf("stale new request: %v", err)
	}
	for range 3 {
		if err := coordinator.Apply(ctx, decision); err != nil {
			t.Fatal(err)
		}
	}
	if credit.calls != 0 {
		t.Fatal("decision replay dispatched Credit")
	}
	if _, err := provider.Down(ctx); err == nil {
		t.Fatal("downgrade discarded account authority")
	}
	operation := *stored.PendingCancellationID
	if err := NewCanceller(repo, credit, AssignmentOptions{Now: func() time.Time { return now }}).ProcessCancellation(ctx, operation); err != nil {
		t.Fatal(err)
	}
	newer := decision
	newer.ID, newer.Revision, newer.Suspended, newer.Reason = uuid.NewString(), 2, false, "reinstated"
	if err := coordinator.Apply(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Apply(ctx, decision); err != nil {
		t.Fatal(err)
	}
	// Old delivery must not undo reinstatement or act on a later assignment.
	if result, err := assigner.AcceptOrder(ctx, courier, o.ID, uuid.NewString()); err != nil || result.Phase != AssignmentSucceeded {
		t.Fatalf("reinstated: %+v %v", result, err)
	}
	if _, err := NewProgress(NewMutator(repo)).ConfirmPickup(ctx, courier, o.ID); err != nil {
		t.Fatal(err)
	}
	newer.ID, newer.Revision, newer.Suspended = uuid.NewString(), 3, true
	if err := coordinator.Apply(ctx, newer); err != nil {
		t.Fatal(err)
	}
	after, err := repo.Get(ctx, o.ID)
	if err != nil || after.Status != Delivering || after.PendingCancellationID != nil || !after.IsAssignedTo(courier.ID) {
		t.Fatalf("pickup-first changed custody: %+v %v", after, err)
	}

	// Adjust has crossed the remote boundary but its reply has not returned.
	// Suspension must fence that exact generation before the late reply arrives.
	inflight := createOpen(uuid.NewString())
	victim := Caller{ID: uuid.NewString()}
	paused := &pausedSuspensionCredit{suspensionCredit: &suspensionCredit{view: AssignmentReservation{OrderID: inflight.ID, RequesterID: inflight.RequesterID, OriginalAmount: 15, ReservedAmount: 15, State: "held"}}, entered: make(chan struct{}), release: make(chan struct{})}
	inflightAssigner := NewAssigner(repo, paused, AssignmentOptions{Now: func() time.Time { return now }})
	type answer struct {
		result AssignmentResult
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		result, err := inflightAssigner.AcceptOrder(ctx, victim, inflight.ID, uuid.NewString())
		done <- answer{result, err}
	}()
	select {
	case <-paused.entered:
	case a := <-done:
		t.Fatalf("Adjust did not start: %+v %v", a.result, a.err)
	case <-time.After(3 * time.Second):
		t.Fatal("Adjust did not start")
	}
	inflightDecision := AccountSuspensionDecision{ID: uuid.NewString(), UserID: victim.ID, AdminID: decision.AdminID, Reason: "explicit suspension during Adjust", Revision: 1, Suspended: true}
	applyErr := NewAccountSuspender(repo, paused, AssignmentOptions{Now: func() time.Time { return now }}).Apply(ctx, inflightDecision)
	close(paused.release)
	if applyErr != nil {
		t.Fatal(applyErr)
	}
	a := <-done
	if a.err != nil || a.result.Phase == AssignmentSucceeded {
		t.Fatalf("late Adjust assigned suspended courier: %+v %v", a.result, a.err)
	}
	if err := inflightAssigner.ProcessAssignment(ctx, a.result.AttemptID); err != nil {
		t.Fatal(err)
	}
	if err := inflightAssigner.ProcessAssignment(ctx, a.result.AttemptID); err != nil {
		t.Fatal(err)
	}
	current, err := repo.Get(ctx, inflight.ID)
	if err != nil || current.CourierID != nil || current.Status != AwaitingCourier || paused.calls != 1 {
		t.Fatalf("pending assignment compensation: %+v %v calls=%d", current, err, paused.calls)
	}
}

type pausedSuspensionCredit struct {
	*suspensionCredit
	entered, release chan struct{}
}

func (c *pausedSuspensionCredit) Adjust(ctx context.Context, in AdjustmentIntent) error {
	if err := c.suspensionCredit.Adjust(ctx, in); err != nil {
		return err
	}
	close(c.entered)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.release:
		return nil
	}
}
