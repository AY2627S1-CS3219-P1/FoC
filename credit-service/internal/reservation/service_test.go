package reservation_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
)

const (
	orderID     = "11111111-1111-4111-8111-111111111111"
	otherOrder  = "11111111-1111-4111-8111-222222222222"
	requester   = "22222222-2222-4222-8222-222222222222"
	courier     = "33333333-3333-4333-8333-333333333333"
	offerID     = "44444444-4444-4444-8444-444444444444"
	otherOffer  = "44444444-4444-4444-8444-555555555555"
	thirdOffer  = "44444444-4444-4444-8444-666666666666"
	otherPerson = "55555555-5555-4555-8555-555555555555"
)

var now = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func newService() (*reservation.Service, *memoryRepository) {
	repo := newMemoryRepository()
	return reservation.NewService(repo, func() time.Time { return now }), repo
}

func expect(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

func must(t *testing.T) func(reservation.Reservation, error) reservation.Reservation {
	t.Helper()
	return func(r reservation.Reservation, err error) reservation.Reservation {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
}

func TestReserveHoldsCreditsOnceAndReplays(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	r := must(t)(s.Reserve(ctx, orderID, requester, 30))
	if r.Status != reservation.Held || r.OriginalAmount != 30 || r.ReservedAmount != 30 || r.Revision != 1 {
		t.Fatalf("reserve: %+v", r)
	}
	if a := repo.balance(requester); a.Balance != 100 || a.Reserved != 30 {
		t.Fatalf("account after reserve: %+v", a)
	}
	again := must(t)(s.Reserve(ctx, orderID, requester, 30))
	if again.Revision != 1 || repo.balance(requester).Reserved != 30 {
		t.Fatalf("replay changed state: %+v %+v", again, repo.balance(requester))
	}
	_, err := s.Reserve(ctx, orderID, requester, 31)
	expect(t, err, reservation.ErrIdempotencyConflict)
	repo.checkLedger(t)
}

func TestReserveRejectsAboveAvailableAndReplaysTheRejection(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	must(t)(s.Reserve(ctx, otherOrder, requester, 80))
	_, err := s.Reserve(ctx, orderID, requester, 21)
	expect(t, err, reservation.ErrInsufficientCredits)
	must(t)(s.ReleaseReservation(ctx, otherOrder, reservation.Failed))
	// The first outcome is stored, so freeing credits does not change it.
	_, err = s.Reserve(ctx, orderID, requester, 21)
	expect(t, err, reservation.ErrInsufficientCredits)
	_, err = s.Reserve(ctx, orderID, requester, 20)
	expect(t, err, reservation.ErrIdempotencyConflict)
	repo.checkLedger(t)
}

func TestReleaseBeforeReserveFencesTheReservation(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	tomb := must(t)(s.ReleaseReservation(ctx, orderID, reservation.Expired))
	if tomb.Status != reservation.Released || tomb.RequesterID != "" || tomb.Settlement.ReleaseReason != reservation.Expired {
		t.Fatalf("tombstone: %+v", tomb)
	}
	_, err := s.Reserve(ctx, orderID, requester, 10)
	expect(t, err, reservation.ErrReservationReleased)
	must(t)(s.ReleaseReservation(ctx, orderID, reservation.Expired))
	_, err = s.ReleaseReservation(ctx, orderID, reservation.Failed)
	expect(t, err, reservation.ErrIdempotencyConflict)
	got := must(t)(s.GetReservation(ctx, orderID))
	if got.Status != reservation.Released {
		t.Fatalf("get: %+v", got)
	}
	repo.checkLedger(t)
}

func TestAdjustRaisesTheHoldAndRevertRestoresIt(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	must(t)(s.Reserve(ctx, orderID, requester, 30))
	r := must(t)(s.AdjustReservation(ctx, orderID, offerID, courier, 50))
	if r.Status != reservation.Assigned || r.ReservedAmount != 50 || r.Adjustment == nil ||
		r.Adjustment.CourierID != courier || r.Adjustment.PreviousAmount != 30 || r.Adjustment.OfferAmount != 50 {
		t.Fatalf("adjust: %+v %+v", r, r.Adjustment)
	}
	_, err := s.AdjustReservation(ctx, orderID, otherOffer, courier, 40)
	expect(t, err, reservation.ErrAdjustmentPending)
	_, err = s.AdjustReservation(ctx, orderID, offerID, courier, 60)
	expect(t, err, reservation.ErrIdempotencyConflict)

	reverted := must(t)(s.RevertAdjustment(ctx, orderID, offerID))
	if reverted.Status != reservation.Held || reverted.ReservedAmount != 30 || reverted.Adjustment != nil {
		t.Fatalf("revert: %+v", reverted)
	}
	if repo.balance(requester).Reserved != 30 {
		t.Fatalf("account after revert: %+v", repo.balance(requester))
	}
	// Replays return the snapshot each call produced.
	replay := must(t)(s.AdjustReservation(ctx, orderID, offerID, courier, 50))
	if replay.Status != reservation.Assigned || replay.Revision != 2 || replay.Adjustment == nil {
		t.Fatalf("adjust replay: %+v", replay)
	}
	if again := must(t)(s.RevertAdjustment(ctx, orderID, offerID)); again.Revision != 3 {
		t.Fatalf("revert replay: %+v", again)
	}
	must(t)(s.AdjustReservation(ctx, orderID, otherOffer, courier, 40))
	repo.checkLedger(t)
}

func TestLowerOfferKeepsTheOriginalHold(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	must(t)(s.Reserve(ctx, orderID, requester, 50))
	r := must(t)(s.AdjustReservation(ctx, orderID, offerID, courier, 30))
	if r.ReservedAmount != 50 || r.Adjustment.OfferAmount != 30 {
		t.Fatalf("adjust: %+v %+v", r, r.Adjustment)
	}
	// Spend every remaining credit; the revert still succeeds.
	must(t)(s.Reserve(ctx, otherOrder, requester, 50))
	must(t)(s.RevertAdjustment(ctx, orderID, offerID))
	if a := repo.balance(requester); a.Reserved != 100 {
		t.Fatalf("account: %+v", a)
	}
	repo.checkLedger(t)
}

func TestAdjustRejectsAboveAvailable(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	must(t)(s.Reserve(ctx, orderID, requester, 30))
	_, err := s.AdjustReservation(ctx, orderID, offerID, courier, 131)
	expect(t, err, reservation.ErrInsufficientCredits)
	_, err = s.AdjustReservation(ctx, orderID, offerID, courier, 131)
	expect(t, err, reservation.ErrInsufficientCredits)
	r := must(t)(s.AdjustReservation(ctx, orderID, otherOffer, courier, 100))
	if r.ReservedAmount != 100 {
		t.Fatalf("adjust: %+v", r)
	}
	repo.checkLedger(t)
}

func TestRevertBeforeAdjustFencesTheOffer(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	_, err := s.RevertAdjustment(ctx, orderID, offerID)
	expect(t, err, reservation.ErrReservationNotFound)
	must(t)(s.Reserve(ctx, orderID, requester, 30))
	r := must(t)(s.RevertAdjustment(ctx, orderID, offerID))
	if r.Status != reservation.Held || r.Revision != 1 {
		t.Fatalf("revert tombstone: %+v", r)
	}
	_, err = s.AdjustReservation(ctx, orderID, offerID, courier, 30)
	expect(t, err, reservation.ErrAdjustmentReverted)
	repo.checkLedger(t)
}

func TestReleaseReturnsTheWholeHold(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	must(t)(s.Reserve(ctx, orderID, requester, 30))
	must(t)(s.AdjustReservation(ctx, orderID, offerID, courier, 45))
	r := must(t)(s.ReleaseReservation(ctx, orderID, reservation.CancelledBeforePickup))
	if r.Status != reservation.Released || r.ReservedAmount != 0 || r.Settlement.ReturnedToRequester != 45 || r.Settlement.PaidToCourier != 0 {
		t.Fatalf("release: %+v %+v", r, r.Settlement)
	}
	if a := repo.balance(requester); a.Balance != 100 || a.Reserved != 0 {
		t.Fatalf("account: %+v", a)
	}
	// Nothing is left to undo after a release.
	after := must(t)(s.RevertAdjustment(ctx, orderID, offerID))
	if after.Revision != r.Revision {
		t.Fatalf("revert after release: %+v", after)
	}
	_, err := s.AdjustReservation(ctx, orderID, otherOffer, courier, 10)
	expect(t, err, reservation.ErrReservationReleased)
	repo.checkLedger(t)
}

func completed(picked, delivered time.Time, allowed time.Duration) reservation.OrderOutcome {
	return reservation.OrderOutcome{
		OrderID: orderID, Type: reservation.OrderCompleted, RequesterID: requester, CourierID: courier,
		PickedUpAt: picked, DeliveredAt: delivered, DeliveryDuration: allowed,
	}
}

func assigned(t *testing.T, original, offer int64) (*reservation.Service, *memoryRepository) {
	t.Helper()
	s, repo := newService()
	must(t)(s.Reserve(context.Background(), orderID, requester, original))
	must(t)(s.AdjustReservation(context.Background(), orderID, offerID, courier, offer))
	return s, repo
}

func TestCompletedOrderPaysTheLatenessAdjustedReward(t *testing.T) {
	tests := []struct {
		name            string
		original, offer int64
		elapsed         time.Duration
		paid, refunded  int64
	}{
		{"on time", 40, 40, 30 * time.Minute, 40, 0},
		{"late", 40, 40, 90 * time.Minute, 20, 20},
		{"twice the duration", 40, 40, 2 * time.Hour, 0, 40},
		{"lower offer refunds the extra hold", 50, 30, time.Hour, 30, 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s, repo := assigned(t, tt.original, tt.offer)
			r, applied, err := s.ApplyOutcome(ctx, completed(now, now.Add(tt.elapsed), time.Hour))
			if err != nil || !applied {
				t.Fatalf("apply: %v %v", applied, err)
			}
			if r.Status != reservation.Settled || r.Settlement.PaidToCourier != tt.paid || r.Settlement.ReturnedToRequester != tt.refunded ||
				r.Settlement.Completion == nil {
				t.Fatalf("settlement: %+v", r.Settlement)
			}
			if a := repo.balance(requester); a.Balance != 100-tt.paid || a.Reserved != 0 {
				t.Fatalf("requester: %+v", a)
			}
			if a := repo.balance(courier); a.Balance != 100+tt.paid {
				t.Fatalf("courier: %+v", a)
			}
			repo.checkLedger(t)
		})
	}
}

func TestOutcomesSettleAtMostOnce(t *testing.T) {
	ctx := context.Background()
	s, repo := assigned(t, 40, 40)
	cancelled := reservation.OrderOutcome{OrderID: orderID, Type: reservation.OrderCancelledAfterPickup, RequesterID: requester, CourierID: courier}
	r, applied, err := s.ApplyOutcome(ctx, cancelled)
	if err != nil || !applied || r.Settlement.PaidToCourier != 40 {
		t.Fatalf("apply: %+v %v %v", r, applied, err)
	}
	if _, applied, err = s.ApplyOutcome(ctx, cancelled); err != nil || applied {
		t.Fatalf("duplicate: %v %v", applied, err)
	}
	_, _, err = s.ApplyOutcome(ctx, completed(now, now, time.Hour))
	expect(t, err, reservation.ErrOutcomeMismatch)
	_, _, err = s.ApplyOutcome(ctx, reservation.OrderOutcome{OrderID: orderID, Type: reservation.OrderFailed, RequesterID: requester})
	expect(t, err, reservation.ErrOutcomeMismatch)
	_, err = s.ReleaseReservation(ctx, orderID, reservation.Failed)
	expect(t, err, reservation.ErrReservationSettled)
	_, err = s.RevertAdjustment(ctx, orderID, offerID)
	expect(t, err, reservation.ErrReservationSettled)
	if a := repo.balance(courier); a.Balance != 140 {
		t.Fatalf("courier paid twice: %+v", a)
	}
	repo.checkLedger(t)
}

func TestReleaseOutcomesReturnTheHold(t *testing.T) {
	for _, outcome := range []reservation.OutcomeType{reservation.OrderCancelledBeforePickup, reservation.OrderExpired, reservation.OrderFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			ctx := context.Background()
			s, repo := assigned(t, 40, 60)
			event := reservation.OrderOutcome{OrderID: orderID, Type: outcome, RequesterID: requester}
			r, applied, err := s.ApplyOutcome(ctx, event)
			if err != nil || !applied || r.Status != reservation.Released || r.Settlement.ReturnedToRequester != 60 {
				t.Fatalf("apply: %+v %v %v", r, applied, err)
			}
			if _, applied, err = s.ApplyOutcome(ctx, event); err != nil || applied {
				t.Fatalf("duplicate: %v %v", applied, err)
			}
			if a := repo.balance(requester); a.Balance != 100 || a.Reserved != 0 {
				t.Fatalf("requester: %+v", a)
			}
			repo.checkLedger(t)
		})
	}
}

func TestMismatchedOutcomesChangeNothing(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	must(t)(s.Reserve(ctx, orderID, requester, 40))
	_, _, err := s.ApplyOutcome(ctx, completed(now, now, time.Hour))
	expect(t, err, reservation.ErrReservationNotAssigned)
	must(t)(s.AdjustReservation(ctx, orderID, offerID, courier, 40))
	wrongCourier := completed(now, now, time.Hour)
	wrongCourier.CourierID = otherPerson
	_, _, err = s.ApplyOutcome(ctx, wrongCourier)
	expect(t, err, reservation.ErrOutcomeMismatch)
	wrongRequester := completed(now, now, time.Hour)
	wrongRequester.RequesterID = otherPerson
	_, _, err = s.ApplyOutcome(ctx, wrongRequester)
	expect(t, err, reservation.ErrOutcomeMismatch)
	_, _, err = s.ApplyOutcome(ctx, completed(now, now.Add(-time.Second), time.Hour))
	expect(t, err, reservation.ErrInvalidArgument)
	missing := completed(now, now, time.Hour)
	missing.OrderID = otherOrder
	_, _, err = s.ApplyOutcome(ctx, missing)
	expect(t, err, reservation.ErrReservationNotFound)
	if r := must(t)(s.GetReservation(ctx, orderID)); r.Status != reservation.Assigned {
		t.Fatalf("reservation changed: %+v", r)
	}
	repo.checkLedger(t)
}

func TestReleaseOutcomeForAnUnknownOrderLeavesATombstone(t *testing.T) {
	ctx := context.Background()
	s, repo := newService()
	r, applied, err := s.ApplyOutcome(ctx, reservation.OrderOutcome{OrderID: orderID, Type: reservation.OrderExpired, RequesterID: requester})
	if err != nil || !applied || r.RequesterID != "" {
		t.Fatalf("apply: %+v %v %v", r, applied, err)
	}
	_, err = s.Reserve(ctx, orderID, requester, 10)
	expect(t, err, reservation.ErrReservationReleased)
	repo.checkLedger(t)
}

func TestInvalidInputIsRejected(t *testing.T) {
	ctx := context.Background()
	s, _ := newService()
	_, err := s.Reserve(ctx, "not-a-uuid", requester, 10)
	expect(t, err, reservation.ErrInvalidArgument)
	_, err = s.Reserve(ctx, orderID, requester, 0)
	expect(t, err, reservation.ErrInvalidArgument)
	_, err = s.ReleaseReservation(ctx, orderID, "LOST")
	expect(t, err, reservation.ErrInvalidArgument)
	_, _, err = s.ApplyOutcome(ctx, reservation.OrderOutcome{OrderID: orderID, Type: "REJECTED", RequesterID: requester})
	expect(t, err, reservation.ErrInvalidArgument)
	_, err = s.GetReservation(ctx, orderID)
	expect(t, err, reservation.ErrReservationNotFound)
}
