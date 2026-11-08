package order

import "context"

// OutstandingWorkReason mirrors the stable reason categories of
// order.v1.OutstandingWorkReason. Order maps them at the RPC boundary.
type OutstandingWorkReason string

const (
	// OutstandingNonterminalOrder: requester or current courier on a
	// nonterminal order, including pending reservation through delivered.
	OutstandingNonterminalOrder OutstandingWorkReason = "nonterminal_order"
	// OutstandingOpenOffer: an open or accepting offer made by the user that
	// has not been withdrawn, voided or selected into an assignment.
	OutstandingOpenOffer OutstandingWorkReason = "open_offer"
	// OutstandingPendingAdmission: a durable record that can still create a
	// new obligation for the user, such as a creation still reserving credit
	// or an assignment still applying an offer.
	OutstandingPendingAdmission OutstandingWorkReason = "pending_admission"
	// OutstandingPendingWorkflow: an unresolved creation, assignment or
	// cancellation recovery record involving the user. A former courier's
	// fully resolved assignment history alone is not outstanding.
	OutstandingPendingWorkflow OutstandingWorkReason = "pending_workflow"
)

// OutstandingWork reports the drain check for one user on one database
// snapshot. Reasons is empty when nothing is outstanding.
type OutstandingWork struct {
	Reasons []OutstandingWorkReason
}

// HasOutstandingWork reports whether any reason blocks finalization.
func (w OutstandingWork) HasOutstandingWork() bool {
	return len(w.Reasons) > 0
}

// DrainReader provides the read-only account-deletion outstanding-work check.
// It never mutates state or reserves the user.
type DrainReader interface {
	OutstandingWork(ctx context.Context, userID string) (OutstandingWork, error)
}
