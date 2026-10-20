// Package account reads users' credit accounts and ledgers. Commands open an
// account with the starting credits the first time they see a user; reads
// never open one.
package account

import (
	"errors"
	"time"
)

// StartingCredits are granted once, when the account is opened (C1.3.1).
const StartingCredits int64 = 100

const (
	DefaultPageSize int32 = 20
	MaxPageSize     int32 = 100
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	// The user has no account yet: Credit has not consumed their users.create
	// event and they have not reserved or been paid.
	ErrAccountNotFound = errors.New("account not found")
)

type Account struct {
	UserID   string
	Balance  int64
	Reserved int64
}

// Available never becomes negative (C1.1.2).
func (a Account) Available() int64 { return a.Balance - a.Reserved }

type Kind string

const (
	Grant   Kind = "GRANT"
	Reserve Kind = "RESERVE"
	Adjust  Kind = "ADJUST"
	Revert  Kind = "REVERT"
	Release Kind = "RELEASE"
	Payout  Kind = "PAYOUT"
	Refund  Kind = "REFUND"
)

type LedgerEntry struct {
	ID            string
	Kind          Kind
	BalanceDelta  int64
	ReservedDelta int64
	// Empty for grants.
	OrderID string
	// Set for adjustments and reverts.
	AssignmentID string
	CreatedAt    time.Time
}

// Page is one-based. Zero values select the first page and the default size.
type Page struct{ Number, Size int32 }

type PageInfo struct {
	Page, PageSize int32
	TotalItems     int64
	TotalPages     int32
}

type LedgerPage struct {
	Entries  []LedgerEntry
	PageInfo PageInfo
}
