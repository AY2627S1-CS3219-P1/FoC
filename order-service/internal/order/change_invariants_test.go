package order

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNewChangeRejectsCourierCancellationThatDropsSettlementFacts(t *testing.T) {
	pickedUpAt := time.Date(2026, 4, 1, 9, 30, 0, 0, time.UTC)
	courierID := "courier"
	before := Order{
		ID: "o1", RequesterID: "requester", CourierID: &courierID,
		Status: Delivering, ReservedCredits: 500, DeliveryDuration: 25 * time.Minute,
		PickedUpAt: &pickedUpAt, Revision: 3,
	}
	after := before
	after.Status = Failed
	after.Revision++
	after.UpdatedAt = pickedUpAt.Add(time.Minute)
	// A valid courier cancellation keeps participant and settlement context.
	// Losing these fields would also suppress the post-pickup warning fact.
	after.CourierID = nil
	after.PickedUpAt = nil
	after.ReservedCredits = 0

	idCalls := 0
	_, err := NewChange(&before, after, Actor{Kind: User, ID: courierID}, Cancel, nil, func() string {
		idCalls++
		return "event"
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered cancellation error = %v, want ErrInvalid", err)
	}
	if idCalls != 0 {
		t.Fatalf("invalid cancellation allocated %d fact IDs", idCalls)
	}
}

func TestNewChangeTerminalMetadataCannotAlterSettlementFields(t *testing.T) {
	pickedUpAt := time.Date(2026, 4, 1, 9, 30, 0, 0, time.UTC)
	courierID := "courier"
	before := Order{
		ID: "o1", RequesterID: "requester", CourierID: &courierID,
		Status: Cancelled, ReservedCredits: 500, DeliveryDuration: 25 * time.Minute,
		PickedUpAt: &pickedUpAt, Revision: 4,
	}
	after := before
	after.Revision++
	after.UpdatedAt = pickedUpAt.Add(time.Hour)
	after.ItemLocation = strptr("front desk")
	after.ReservedCredits = 0

	_, err := NewChange(&before, after, Actor{Kind: User, ID: courierID}, RecordItemLocation, nil, testIDs())
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("tampered terminal metadata error = %v, want ErrInvalid", err)
	}
}

func TestSameStatusAssignmentRepeatCannotCommit(t *testing.T) {
	courierID := "courier"
	current := Order{ID: "o1", CourierID: &courierID, Status: AwaitingPickup, Revision: 9}
	events := []string{}
	repo := &repoFake{tx: &txFake{events: &events, order: current}}
	err := NewMutator(repo).Mutate(context.Background(), "o1", func(locked Order, _ []Offer) (*Change, error) {
		after := locked
		after.Revision++
		after.UpdatedAt = time.Now().UTC()
		change, err := NewChange(&locked, after, Actor{Kind: User, ID: courierID}, Assign, nil, testIDs())
		if err != nil {
			return nil, err
		}
		return &change, nil
	})
	if !errors.Is(err, ErrPrecondition) {
		t.Fatalf("same-status assignment error = %v, want ErrPrecondition", err)
	}
	if repo.commits != 0 || repo.rollbacks != 1 || repo.tx.applied != 0 {
		t.Fatalf("same-status assignment committed: commits=%d rollbacks=%d apply=%d", repo.commits, repo.rollbacks, repo.tx.applied)
	}
}
