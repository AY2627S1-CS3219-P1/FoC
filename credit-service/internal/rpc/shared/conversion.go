package shared

import (
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	creditv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/credit/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Reservations

var reservationStatuses = map[reservation.Status]creditv1.ReservationStatus{
	reservation.Held:     creditv1.ReservationStatus_RESERVATION_STATUS_HELD,
	reservation.Assigned: creditv1.ReservationStatus_RESERVATION_STATUS_ASSIGNED,
	reservation.Released: creditv1.ReservationStatus_RESERVATION_STATUS_RELEASED,
	reservation.Settled:  creditv1.ReservationStatus_RESERVATION_STATUS_SETTLED,
}

var adjustmentStatuses = map[reservation.AdjustmentStatus]creditv1.AdjustmentStatus{
	reservation.Applied:  creditv1.AdjustmentStatus_ADJUSTMENT_STATUS_APPLIED,
	reservation.Reverted: creditv1.AdjustmentStatus_ADJUSTMENT_STATUS_REVERTED,
}

var settlementOutcomes = map[reservation.SettlementOutcome]creditv1.SettlementOutcome{
	reservation.OutcomeReleased:             creditv1.SettlementOutcome_SETTLEMENT_OUTCOME_RELEASED,
	reservation.OutcomeCompleted:            creditv1.SettlementOutcome_SETTLEMENT_OUTCOME_COMPLETED,
	reservation.OutcomeCancelledAfterPickup: creditv1.SettlementOutcome_SETTLEMENT_OUTCOME_CANCELLED_AFTER_PICKUP,
}

var releaseReasons = map[reservation.ReleaseReason]creditv1.ReleaseReason{
	reservation.CancelledBeforePickup: creditv1.ReleaseReason_RELEASE_REASON_CANCELLED_BEFORE_PICKUP,
	reservation.Expired:               creditv1.ReleaseReason_RELEASE_REASON_EXPIRED,
	reservation.Failed:                creditv1.ReleaseReason_RELEASE_REASON_FAILED,
}

// ReleaseReason returns the domain reason, or "" for a value the domain
// rejects as an invalid argument.
func ReleaseReason(r creditv1.ReleaseReason) reservation.ReleaseReason {
	for domain, proto := range releaseReasons {
		if proto == r {
			return domain
		}
	}
	return ""
}

// Reservation describes r in the contract's terms: the order ID is the
// request_id and an assignment ID is the offer_id.
func Reservation(r reservation.Reservation) *creditv1.Reservation {
	out := &creditv1.Reservation{
		RequestId:      r.OrderID,
		RequesterId:    r.RequesterID,
		Status:         reservationStatuses[r.Status],
		OriginalAmount: r.OriginalAmount,
		ReservedAmount: r.ReservedAmount,
		Revision:       r.Revision,
		CreatedAt:      timestamp(r.CreatedAt),
		UpdatedAt:      timestamp(r.UpdatedAt),
	}

	if a := r.Adjustment; a != nil {
		out.ActiveAdjustment = &creditv1.Adjustment{
			OfferId:        a.AssignmentID,
			CourierId:      a.CourierID,
			PreviousAmount: a.PreviousAmount,
			OfferAmount:    a.OfferAmount,
			Status:         adjustmentStatuses[a.Status],
		}
	}
	if s := r.Settlement; s != nil {
		out.Settlement = &creditv1.Settlement{
			Outcome:             settlementOutcomes[s.Outcome],
			PaidToCourier:       s.PaidToCourier,
			ReturnedToRequester: s.ReturnedToRequester,
			SettledAt:           timestamp(s.SettledAt),
			ReleaseReason:       releaseReasons[s.ReleaseReason],
		}
	}
	return out
}

// Accounts

var ledgerKinds = map[account.Kind]creditv1.LedgerEntryKind{
	account.Grant:   creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_GRANT,
	account.Reserve: creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_RESERVE,
	account.Adjust:  creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_ADJUST,
	account.Revert:  creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_REVERT,
	account.Release: creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_RELEASE,
	account.Payout:  creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_PAYOUT,
	account.Refund:  creditv1.LedgerEntryKind_LEDGER_ENTRY_KIND_REFUND,
}

func Account(a account.Account) *creditv1.Account {
	return &creditv1.Account{
		UserId:    a.UserID,
		Balance:   a.Balance,
		Reserved:  a.Reserved,
		Available: a.Available(),
	}
}

func LedgerPage(p account.LedgerPage) *creditv1.ListMyLedgerEntriesResponse {
	entries := make([]*creditv1.LedgerEntry, len(p.Entries))
	for i, e := range p.Entries {
		entries[i] = &creditv1.LedgerEntry{
			Id:            e.ID,
			Kind:          ledgerKinds[e.Kind],
			BalanceDelta:  e.BalanceDelta,
			ReservedDelta: e.ReservedDelta,
			RequestId:     optional(e.OrderID),
			OfferId:       optional(e.AssignmentID),
			CreatedAt:     timestamp(e.CreatedAt),
		}
	}

	return &creditv1.ListMyLedgerEntriesResponse{
		Entries: entries,
		PageInfo: &creditv1.PageInfo{
			Page:       p.PageInfo.Page,
			PageSize:   p.PageInfo.PageSize,
			TotalItems: p.PageInfo.TotalItems,
			TotalPages: p.PageInfo.TotalPages,
		},
	}
}

func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func optional(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
