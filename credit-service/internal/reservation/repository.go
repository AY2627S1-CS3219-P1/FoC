package reservation

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
)

// Repository runs a unit of work in one transaction and rolls it back when f
// returns an error.
type Repository interface {
	Within(ctx context.Context, f func(Tx) error) error
}

// Tx reads used before writes lock the row until the unit of work ends.
// Reservation, Assignment and Operation return nil when the row does not exist.
type Tx interface {
	// LockOrder serializes all work on one order, including unknown orders.
	LockOrder(ctx context.Context, orderID string) error
	Operation(ctx context.Context, key OperationKey) (*Operation, error)
	SaveOperation(ctx context.Context, key OperationKey, op Operation) error
	Reservation(ctx context.Context, orderID string) (*Reservation, error)
	Revision(ctx context.Context, orderID string, revision int64) (Reservation, error)
	// InsertReservation and UpdateReservation return the reservation as
	// stored, which is what a replay reads back.
	InsertReservation(ctx context.Context, r Reservation) (Reservation, error)
	// UpdateReservation stores r as the revision after expectedRevision. It
	// ignores r.Revision and r.UpdatedAt, which the database assigns, and
	// fails with ErrAborted unless the stored revision is expectedRevision.
	UpdateReservation(ctx context.Context, r Reservation, expectedRevision int64) (Reservation, error)
	Assignment(ctx context.Context, orderID, assignmentID string) (*Assignment, error)
	InsertAssignment(ctx context.Context, a Assignment) error
	RevertAssignment(ctx context.Context, orderID, assignmentID string, at time.Time) error
	// LockAccounts opens missing accounts with the starting credits and locks
	// them in a fixed order.
	LockAccounts(ctx context.Context, at time.Time, userIDs ...string) (map[string]account.Account, error)
	// Post applies each entry to its account and appends it to the ledger,
	// skipping entries that change nothing.
	Post(ctx context.Context, at time.Time, entries ...Entry) error
}

type OperationKind string

const (
	OpReserve OperationKind = "RESERVE"
	OpAdjust  OperationKind = "ADJUST"
	OpRevert  OperationKind = "REVERT"
	OpRelease OperationKind = "RELEASE"
)

// OperationKey identifies a mutating call. AssignmentID is set only for
// adjustments and reverts.
type OperationKey struct {
	Kind         OperationKind
	OrderID      string
	AssignmentID string
}

// Operation records a call's first outcome: the revision it produced, or a
// stored INSUFFICIENT_CREDITS rejection.
type Operation struct {
	RequestHash  string
	Revision     int64
	Insufficient bool
	CreatedAt    time.Time
}

// Entry is a ledger entry with the account change it records.
type Entry struct {
	UserID        string
	Kind          account.Kind
	BalanceDelta  int64
	ReservedDelta int64
	OrderID       string
	AssignmentID  string
}
