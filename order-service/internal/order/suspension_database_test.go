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
func TestSuspensionRemovalDatabase(t *testing.T) {
	dsn := os.Getenv("SUSPENSION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("SUSPENSION_TEST_DATABASE_URL not set; requires disposable PostgreSQL 18")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
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
	defer sqlDB.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, os.DirFS("../../database/schema"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewPostgresRepository(pool)
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
	if _, err := provider.Down(ctx); err == nil {
		t.Fatal("downgrade discarded pending suspension identity")
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
