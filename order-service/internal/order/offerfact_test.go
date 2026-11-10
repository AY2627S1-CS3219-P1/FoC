package order

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func offerOrder() Order {
	at := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	return Order{
		ID: "4f6d2c5e-8a7b-4c3d-9e1f-0a2b3c4d5e6f", RequesterID: "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d",
		Status: AwaitingCourier, Revision: 3, RewardCredits: 10, ReservedCredits: 10,
		ExpiresAt: at.Add(time.Hour), CreatedAt: at, UpdatedAt: at,
	}
}

func proposal(t *testing.T, before Order, courier string, credits int64) (Order, json.RawMessage) {
	t.Helper()
	after := cloneOrder(before)
	after.Revision++
	after.UpdatedAt = before.UpdatedAt.Add(time.Minute)
	details, err := json.Marshal(
		OfferFact{ID: "1b2c3d4e-5f6a-4b7c-8d9e-0f1a2b3c4d5e", CourierID: courier, Credits: credits},
	)
	if err != nil {
		t.Fatal(err)
	}
	return after, details
}

func TestProposeOfferPublishesTheOffer(t *testing.T) {
	before := offerOrder()
	courier := "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e6f"
	after, details := proposal(t, before, courier, 15)
	change, err := NewChange(&before, after, Actor{Kind: User, ID: courier}, ProposeOffer, details,
		func() string { return "3d4e5f6a-7b8c-4d9e-0f1a-2b3c4d5e6f7a" })
	if err != nil {
		t.Fatal(err)
	}
	if len(change.Facts) != 1 || change.Facts[0].Kind != OfferMade {
		t.Fatalf("facts = %+v", change.Facts)
	}
	var p FactsPayload
	if err := json.Unmarshal(change.Facts[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Offer == nil || p.Offer.Credits != 15 || p.Offer.CourierID != courier || p.RequesterID != before.RequesterID ||
		p.Revision != after.Revision {
		t.Fatalf("payload = %+v", p)
	}
	// Apply rebuilds the change from its inputs; the offer fact must survive that.
	if err := validateChange(&before, change); err != nil {
		t.Fatalf("validateChange: %v", err)
	}
}

func TestProposeOfferRejectsAnOfferThatDoesNotMatch(t *testing.T) {
	before := offerOrder()
	courier := "2c3d4e5f-6a7b-4c8d-9e0f-1a2b3c4d5e6f"
	for name, tc := range map[string]struct {
		courier string
		credits int64
	}{
		"another courier's offer": {"7a6b5c4d-3e2f-4a1b-9c8d-7e6f5a4b3c2d", 15},
		"not above the reward":    {courier, 10},
	} {
		after, details := proposal(t, before, tc.courier, tc.credits)
		_, err := NewChange(&before, after, Actor{Kind: User, ID: courier}, ProposeOffer, details,
			func() string { return "3d4e5f6a-7b8c-4d9e-0f1a-2b3c4d5e6f7a" })
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}
