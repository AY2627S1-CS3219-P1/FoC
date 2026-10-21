package shared_test

import (
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/rpc/shared"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
)

var now = time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)

func TestReservationDescribesEachStatus(t *testing.T) {
	assigned := shared.Reservation(reservation.Reservation{
		OrderID:        "order",
		RequesterID:    "requester",
		Status:         reservation.Assigned,
		OriginalAmount: 20,
		ReservedAmount: 30,
		AssignmentID:   "offer",
		Adjustment: &reservation.Adjustment{
			AssignmentID:   "offer",
			CourierID:      "courier",
			PreviousAmount: 20,
			OfferAmount:    30,
			Status:         reservation.Applied,
		},
		Revision:  2,
		CreatedAt: now,
		UpdatedAt: now,
	})
	a := assigned.GetActiveAdjustment()
	if assigned.GetStatus() != creditv1.ReservationStatus_RESERVATION_STATUS_ASSIGNED || assigned.GetRequestId() != "order" ||
		a.GetOfferId() != "offer" || a.GetCourierId() != "courier" || a.GetPreviousAmount() != 20 ||
		a.GetStatus() != creditv1.AdjustmentStatus_ADJUSTMENT_STATUS_APPLIED || assigned.Settlement != nil ||
		!assigned.GetCreatedAt().AsTime().Equal(now) {
		t.Fatalf("assigned: %v", assigned)
	}

	settled := shared.Reservation(reservation.Reservation{
		OrderID:     "order",
		RequesterID: "requester",
		Status:      reservation.Settled,
		Settlement: &reservation.Settlement{
			Outcome:             reservation.OutcomeCompleted,
			PaidToCourier:       15,
			ReturnedToRequester: 15,
			SettledAt:           now,
		},
	})
	s := settled.GetSettlement()
	if settled.GetStatus() != creditv1.ReservationStatus_RESERVATION_STATUS_SETTLED || settled.ActiveAdjustment != nil ||
		s.GetOutcome() != creditv1.SettlementOutcome_SETTLEMENT_OUTCOME_COMPLETED || s.GetPaidToCourier() != 15 ||
		s.GetReleaseReason() != creditv1.ReleaseReason_RELEASE_REASON_UNSPECIFIED {
		t.Fatalf("settled: %v", settled)
	}

	tombstone := shared.Reservation(reservation.Reservation{
		OrderID: "order",
		Status:  reservation.Released,
		Settlement: &reservation.Settlement{
			Outcome:       reservation.OutcomeReleased,
			SettledAt:     now,
			ReleaseReason: reservation.Expired,
		},
	})
	if tombstone.GetRequesterId() != "" || tombstone.GetStatus() != creditv1.ReservationStatus_RESERVATION_STATUS_RELEASED ||
		tombstone.GetSettlement().GetReleaseReason() != creditv1.ReleaseReason_RELEASE_REASON_EXPIRED {
		t.Fatalf("tombstone: %v", tombstone)
	}
}

func TestReleaseReasonRoundTrips(t *testing.T) {
	for _, r := range []reservation.ReleaseReason{reservation.CancelledBeforePickup, reservation.Expired, reservation.Failed} {
		released := shared.Reservation(reservation.Reservation{Settlement: &reservation.Settlement{ReleaseReason: r}})
		if got := shared.ReleaseReason(released.GetSettlement().GetReleaseReason()); got != r {
			t.Fatalf("%s became %s", r, got)
		}
	}
	if got := shared.ReleaseReason(creditv1.ReleaseReason_RELEASE_REASON_UNSPECIFIED); got != "" {
		t.Fatalf("unspecified became %q", got)
	}
}

func TestLedgerPageKeepsOptionalIDsAbsent(t *testing.T) {
	page := shared.LedgerPage(account.LedgerPage{
		Entries: []account.LedgerEntry{
			{ID: "grant", Kind: account.Grant, BalanceDelta: 100, CreatedAt: now},
			{ID: "adjust", Kind: account.Adjust, ReservedDelta: 10, OrderID: "order", AssignmentID: "offer", CreatedAt: now},
		},
		PageInfo: account.PageInfo{Page: 1, PageSize: 20, TotalItems: 2, TotalPages: 1},
	})

	grant, adjust := page.GetEntries()[0], page.GetEntries()[1]
	if grant.RequestId != nil || grant.OfferId != nil || grant.GetKind() != creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_GRANT {
		t.Fatalf("grant: %v", grant)
	}
	if adjust.GetRequestId() != "order" || adjust.GetOfferId() != "offer" || adjust.GetKind() != creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_ADJUST {
		t.Fatalf("adjust: %v", adjust)
	}
	if info := page.GetPageInfo(); info.GetTotalItems() != 2 || info.GetPageSize() != 20 {
		t.Fatalf("page info: %v", info)
	}
	if a := shared.Account(account.Account{Balance: 100, Reserved: 30}); a.GetAvailable() != 70 {
		t.Fatalf("account: %v", a)
	}
}
