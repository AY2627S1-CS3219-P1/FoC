package order

import (
	"context"
	"errors"
	"testing"
	"time"
)

func progressFixture(status Status) (*Progress, *txFake, Order) {
	created := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	courier := "courier"
	current := Order{
		ID:               "37dbf670-f32c-4cca-9473-5861c274e789",
		RequesterID:      "requester",
		CourierID:        &courier,
		Status:           status,
		RewardCredits:    15,
		ReservedCredits:  15,
		DeliveryDuration: 15 * time.Minute,
		ExpiresAt:        created.Add(time.Hour),
		AssignedAt:       &created,
		Revision:         3,
		CreatedAt:        created,
		UpdatedAt:        created,
	}
	if status == Delivered || status == Completed {
		current.PickedUpAt, current.DeliveredAt = &created, &created
	}
	if status == Completed {
		current.CompletedAt = &created
	}

	events := []string{}
	tx := &txFake{events: &events, order: current}
	p := NewProgress(NewMutator(&repoFake{tx: tx}))
	p.now = func() time.Time { return created.Add(time.Minute + 123) }
	ids := 0
	p.newID = func() string { ids++; return string(rune('a' + ids)) }
	return p, tx, current
}

func factKinds(c Change) []FactKind {
	kinds := make([]FactKind, len(c.Facts))
	for i, f := range c.Facts {
		kinds[i] = f.Kind
	}
	return kinds
}

func TestConfirmPickupByAssignedCourier(t *testing.T) {
	p, tx, current := progressFixture(AwaitingPickup)
	got, err := p.ConfirmPickup(context.Background(), Caller{ID: "courier"}, current.ID)
	if err != nil {
		t.Fatal(err)
	}

	at := current.CreatedAt.Add(time.Minute) // nanoseconds truncated to microseconds
	if got.Status != Delivering || got.Revision != 4 || got.PickedUpAt == nil || !got.PickedUpAt.Equal(at) ||
		!got.UpdatedAt.Equal(at) {
		t.Fatalf("result = %+v", got)
	}
	if tx.applied != 1 || tx.change.Action != Pickup || tx.change.ExpectedRevision != 3 ||
		tx.change.Order.Status != Delivering {
		t.Fatalf("change = %+v", tx.change)
	}
	if kinds := factKinds(tx.change); len(kinds) != 1 || kinds[0] != StatusChanged {
		t.Fatalf("facts = %v", kinds)
	}
	if current.PickedUpAt != nil {
		t.Fatal("locked baseline was modified")
	}
}

func TestConfirmReceiptByRequesterEmitsOutcome(t *testing.T) {
	p, tx, current := progressFixture(Delivered)
	got, err := p.ConfirmReceipt(context.Background(), Caller{ID: "requester"}, current.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != Completed || got.Revision != 4 || got.CompletedAt == nil || got.ClosedAt != nil {
		t.Fatalf("result = %+v", got)
	}
	if kinds := factKinds(tx.change); len(kinds) != 2 || kinds[0] != StatusChanged || kinds[1] != Outcome {
		t.Fatalf("facts = %v", kinds)
	}
}

func TestConfirmReceiptRepeatReturnsCompletedOrderWithoutWriting(t *testing.T) {
	p, tx, current := progressFixture(Completed)
	got, err := p.ConfirmReceipt(context.Background(), Caller{ID: "requester"}, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.applied != 0 || got.Revision != current.Revision || got.Status != Completed {
		t.Fatalf("applied=%d result=%+v", tx.applied, got)
	}
}

func TestConfirmPickupRepeatReturnsDeliveringOrderWithoutWriting(t *testing.T) {
	p, tx, current := progressFixture(Delivering)
	got, err := p.ConfirmPickup(context.Background(), Caller{ID: "courier"}, current.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.applied != 0 || got.Revision != current.Revision || got.Status != Delivering {
		t.Fatalf("applied=%d result=%+v", tx.applied, got)
	}
}

func TestProgressRejectsIneligibleCallersAndStates(t *testing.T) {
	cases := []struct {
		name   string
		status Status
		action func(*Progress) func(context.Context, Caller, string) (Order, error)
		caller Caller
		want   error
	}{
		{"pickup by requester", AwaitingPickup, pickup, Caller{ID: "requester"}, ErrForbidden},
		{"pickup by admin", AwaitingPickup, pickup, Caller{ID: "admin", Admin: true}, ErrForbidden},
		{"pickup by stranger", AwaitingPickup, pickup, Caller{ID: "stranger"}, ErrNotFound},
		{"pickup before assignment", AwaitingCourier, pickup, Caller{ID: "courier"}, ErrPrecondition},
		{"pickup repeat by requester", Delivering, pickup, Caller{ID: "requester"}, ErrPrecondition},
		{"pickup after delivery", Delivered, pickup, Caller{ID: "courier"}, ErrPrecondition},
		{"receipt by courier", Delivered, receipt, Caller{ID: "courier"}, ErrForbidden},
		{"receipt by stranger", Delivered, receipt, Caller{ID: "stranger"}, ErrNotFound},
		{"receipt before delivery", Delivering, receipt, Caller{ID: "requester"}, ErrPrecondition},
		{"receipt repeat by courier", Completed, receipt, Caller{ID: "courier"}, ErrPrecondition},
		{"anonymous", AwaitingPickup, pickup, Caller{}, ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, tx, current := progressFixture(tc.status)
			_, err := tc.action(p)(context.Background(), tc.caller, current.ID)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if tx.applied != 0 {
				t.Fatal("rejected action wrote a change")
			}
		})
	}
}

func TestProgressRejectsMalformedIDBeforeLocking(t *testing.T) {
	p, tx, _ := progressFixture(AwaitingPickup)
	if _, err := p.ConfirmPickup(
		context.Background(),
		Caller{ID: "courier"},
		"not-a-uuid",
	); !errors.Is(
		err,
		ErrInvalid,
	) {
		t.Fatal(err)
	}
	if len(*tx.events) != 0 {
		t.Fatalf("events = %v", *tx.events)
	}
}

func pickup(p *Progress) func(context.Context, Caller, string) (Order, error) { return p.ConfirmPickup }
func receipt(p *Progress) func(context.Context, Caller, string) (Order, error) {
	return p.ConfirmReceipt
}
