package account

import (
	"context"
)

// OutstandingWorkReason mirrors the stable reason categories of
// credit.v1.OutstandingWorkReason. The RPC boundary maps them.
type OutstandingWorkReason string

const (
	// OutstandingReservedBalance: the account holds a nonzero reserved amount.
	OutstandingReservedBalance OutstandingWorkReason = "reserved_balance"
	// OutstandingActiveReservation: HELD or ASSIGNED reservations involving
	// the user as requester or courier.
	OutstandingActiveReservation OutstandingWorkReason = "active_reservation"
	// OutstandingUnsettledAssignment: applied courier assignments whose hold
	// has not settled; reserved = 0 alone does not establish courier safety.
	OutstandingUnsettledAssignment OutstandingWorkReason = "unsettled_assignment"
)

// OutstandingWork reports the drain check for one user on one database
// snapshot. Reasons is empty when nothing is outstanding. A nonexistent
// account is not outstanding.
type OutstandingWork struct {
	Reasons []OutstandingWorkReason
}

// HasOutstandingWork reports whether any reason blocks finalization.
func (w OutstandingWork) HasOutstandingWork() bool {
	return len(w.Reasons) > 0
}

// DrainReader provides the read-only account-deletion outstanding-work check.
// It never mutates state and never opens an account.
type DrainReader interface {
	OutstandingWork(ctx context.Context, userID string) (OutstandingWork, error)
}
