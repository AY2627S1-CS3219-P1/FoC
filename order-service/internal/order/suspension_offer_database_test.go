package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type insufficientSuspensionCredit struct {
	*suspensionCredit
	insufficient bool
}

func (c *insufficientSuspensionCredit) Adjust(ctx context.Context, in AdjustmentIntent) error {
	if c.insufficient {
		return ErrCreditInsufficient
	}
	return c.suspensionCredit.Adjust(ctx, in)
}

// Suspension must retire an in-flight acceptance permanently, including when
// insufficient-credit compensation predates it and reinstatement precedes recovery.
func TestSuspensionDoesNotResurrectInsufficientCreditOfferDatabase(t *testing.T) {
	ctx, pool, repo, _ := suspensionDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	o := Order{
		ID:               uuid.NewString(),
		RequesterID:      uuid.NewString(),
		Status:           PendingReservation,
		Pickup:           LocationSnapshot{LocationID: uuid.NewString(), Name: "Cafe", BuildingName: "COM2"},
		Delivery:         LocationSnapshot{LocationID: uuid.NewString(), Name: "Lobby", BuildingName: "PGP"},
		ItemDescription:  "Lunch",
		RewardCredits:    15,
		ReservedCredits:  15,
		DeliveryDuration: 15 * time.Minute,
		ExpiresAt:        now.Add(time.Hour),
		Revision:         1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	c, err := NewChange(nil, o, Actor{Kind: User, ID: o.RequesterID}, Create, nil, uuid.NewString)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Within(ctx, func(tx Tx) error { return tx.Apply(ctx, c) }); err != nil {
		t.Fatal(err)
	}
	if err := NewMutator(repo).Mutate(ctx, o.ID, func(before Order, _ []Offer) (*Change, error) {
		after := cloneOrder(before)
		after.Status = AwaitingCourier
		after.Revision++
		c, err := NewChange(&before, after, Actor{Kind: System}, ConfirmReservation, nil, uuid.NewString)
		return &c, err
	}); err != nil {
		t.Fatal(err)
	}
	credit := &insufficientSuspensionCredit{
		suspensionCredit: &suspensionCredit{deny: true, view: AssignmentReservation{
			OrderID: o.ID, RequesterID: o.RequesterID, OriginalAmount: 15, ReservedAmount: 15, State: "held",
		}}, insufficient: true,
	}
	options := AssignmentOptions{Now: func() time.Time { return now }}
	assigner := NewAssigner(repo, credit, options)
	courier := Caller{ID: uuid.NewString()}
	requester := Caller{ID: o.RequesterID}
	offer, err := assigner.MakeOffer(ctx, courier, o.ID, 35, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	result, err := assigner.AcceptOffer(ctx, requester, offer.ID)
	if err != nil || result.Phase != RevertPending || result.Rejection != "insufficient_credits" {
		t.Fatalf("expected unfinished insufficient-credit compensation: %+v %v", result, err)
	}
	var before, after AssignmentAttempt
	readAttempt := func() AssignmentAttempt {
		t.Helper()
		var got AssignmentAttempt
		if err := repo.WithinAssignment(ctx, func(tx AssignmentTx) error {
			if _, err := tx.LockOrder(ctx, o.ID); err != nil {
				return err
			}
			if _, err := tx.Relationships(ctx, o.ID); err != nil {
				return err
			}
			var err error
			got, err = tx.FindAssignment(ctx, result.AttemptID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return got
	}
	before = readAttempt()
	decision := AccountSuspensionDecision{
		ID:        uuid.NewString(),
		UserID:    courier.ID,
		AdminID:   uuid.NewString(),
		Revision:  1,
		Suspended: true,
		Reason:    "explicit suspension",
	}
	coordinator := NewAccountSuspender(repo, credit, options)
	if err := coordinator.Apply(ctx, decision); err != nil {
		t.Fatal(err)
	}
	after = readAttempt()
	if before != after {
		t.Fatalf("suspension changed existing compensation: before=%+v after=%+v", before, after)
	}
	var status string
	if err := pool.QueryRow(ctx, "SELECT status FROM offers WHERE id=$1", offer.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(OfferVoid) {
		t.Fatalf("suspension retained accepting offer: %s", status)
	}
	decision.ID, decision.Revision, decision.Suspended = uuid.NewString(), 2, false
	if err := coordinator.Apply(ctx, decision); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	credit.deny = false
	if err := assigner.RecoverAssignmentsOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM offers WHERE id=$1", offer.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != string(OfferVoid) {
		t.Fatalf("recovery resurrected suspended offer: %s", status)
	}
	replay, err := assigner.AcceptOffer(ctx, requester, offer.ID)
	if !errors.Is(err, ErrPrecondition) || replay.Phase == AssignmentSucceeded {
		t.Fatalf("old acceptance resurrected after reinstatement: %+v %v", replay, err)
	}
	credit.insufficient = false
	fresh, err := assigner.MakeOffer(ctx, courier, o.ID, 35, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := assigner.AcceptOffer(ctx, requester, fresh.ID)
	if err != nil || assigned.Phase != AssignmentSucceeded || assigned.AttemptID == result.AttemptID || !assigned.Order.IsAssignedTo(courier.ID) {
		t.Fatalf("fresh reinstated acceptance failed: %+v %v", assigned, err)
	}
}
