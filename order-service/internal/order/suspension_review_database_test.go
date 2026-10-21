package order

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Requirements: the account fence is immediate even when an assignment has no
// immutable recovery identity. Existing B1 refunds must retain their generation
// and lease, and terminal history must not be locked by suspension discovery.
func TestAccountSuspensionReviewDatabase(t *testing.T) {
	ctx, pool, repo, _ := suspensionDatabase(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	courier := Caller{ID: uuid.NewString()}
	credit := &suspensionCredit{}
	assigned := func() (Order, AssignmentResult) {
		t.Helper()
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
		change, err := NewChange(nil, o, Actor{Kind: User, ID: o.RequesterID}, Create, nil, uuid.NewString)
		if err != nil {
			t.Fatal(err)
		}
		if err := repo.Within(ctx, func(tx Tx) error { return tx.Apply(ctx, change) }); err != nil {
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
		credit.view = AssignmentReservation{
			OrderID:        o.ID,
			RequesterID:    o.RequesterID,
			OriginalAmount: 15,
			ReservedAmount: 15,
			State:          "held",
		}
		a, err := NewAssigner(repo, credit, AssignmentOptions{Now: func() time.Time { return now }}).AcceptOrder(ctx, courier, o.ID, uuid.NewString())
		if err != nil || a.Phase != AssignmentSucceeded {
			t.Fatalf("assignment: %+v %v", a, err)
		}
		return o, a
	}

	missing, _ := assigned()
	// Model a legacy snapshot without the matching successful attempt. Do not
	// synthesize another identity or infer a financial reversal from the Order.
	if _, err := pool.Exec(ctx, "UPDATE orders SET assigned_at=assigned_at + interval '1 microsecond' WHERE id=$1", missing.ID); err != nil {
		t.Fatal(err)
	}

	valid, _ := assigned()
	var untouched []string
	for _, status := range []Status{Cancelled, Expired, AwaitingCourier, Completed, Delivered} {
		o, a := assigned()
		phase := "revert_pending"
		if status == Completed || status == Delivered {
			phase = "succeeded"
		}
		if _, err := pool.Exec(ctx, "UPDATE orders SET status=$2 WHERE id=$1", o.ID, string(status)); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, "UPDATE order_assignment_attempts SET phase=$2, generation=7, lease_until=$3 WHERE id=$1", a.AttemptID, phase, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		untouched = append(untouched, a.AttemptID)
	}

	decision := AccountSuspensionDecision{
		ID:        uuid.NewString(),
		UserID:    courier.ID,
		AdminID:   uuid.NewString(),
		Revision:  1,
		Suspended: true,
		Reason:    "explicit suspension",
	}
	if err := NewAccountSuspender(repo, credit, AssignmentOptions{}).Apply(ctx, decision); err != nil {
		t.Fatal(err)
	}
	if err := repo.WithinAccountSuspension(ctx, func(tx AccountSuspensionTx) error {
		d, err := tx.AccountDecision(ctx, courier.ID)
		if err != nil {
			return err
		}
		if d == nil || *d != decision {
			t.Fatalf("fence missing: %+v", d)
		}
		ids, err := tx.AccountOrderIDs(ctx, courier.ID)
		if err != nil {
			return err
		}
		if len(ids) != 2 {
			t.Fatalf("history included in discovery: %v", ids)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	for _, id := range untouched {
		var generation int64
		var lease time.Time
		if err := pool.QueryRow(ctx, "SELECT generation,lease_until FROM order_assignment_attempts WHERE id=$1", id).Scan(&generation, &lease); err != nil {
			t.Fatal(err)
		}
		if generation != 7 || !lease.Equal(now.Add(time.Hour)) {
			t.Fatalf("existing refund/history changed: %d %v", generation, lease)
		}
	}

	for _, o := range []Order{missing, valid} {
		got, err := repo.Get(ctx, o.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != AwaitingPickup || !got.IsAssignedTo(courier.ID) {
			t.Fatalf("assignment changed without recovery: %+v", got)
		}
		if (o.ID == valid.ID) != (got.PendingCancellationID != nil) {
			t.Fatalf("wrong recovery admission: %+v", got)
		}
		_, err = NewProgress(NewMutator(repo)).ConfirmPickup(ctx, courier, o.ID)
		if err == nil || (o.ID == missing.ID && !errors.Is(err, ErrForbidden)) {
			t.Fatalf("pickup did not enforce the account fence: %v", err)
		}
	}
	if credit.calls != 0 {
		t.Fatal("suspension speculated a reversal")
	}

	// A stale instruction must not be acknowledged as applied to User.
	newer := decision
	newer.ID, newer.Revision, newer.Suspended = uuid.NewString(), 2, false
	coordinator := NewAccountSuspender(repo, credit, AssignmentOptions{})
	if err := coordinator.Apply(ctx, newer); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.Apply(ctx, decision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale acknowledgement: %v", err)
	}
}
