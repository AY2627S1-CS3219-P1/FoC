package account

import (
	"context"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/google/uuid"
)

// Posting is one ledger entry with the account change it records.
type Posting struct {
	UserID        uuid.UUID
	Kind          Kind
	BalanceDelta  int64
	ReservedDelta int64
	OrderID       *uuid.UUID
	AssignmentID  *uuid.UUID
}

// Post applies each posting to its account and appends its ledger entry, so a
// user's ledger always sums to their balance and reserved credits. Postings
// that change nothing are skipped. Entries posted together share a timestamp
// and list newest first by their time-ordered IDs. Call it inside the
// caller's transaction.
func Post(ctx context.Context, q *creditdb.Queries, now time.Time, postings ...Posting) error {
	for _, p := range postings {
		if p.BalanceDelta == 0 && p.ReservedDelta == 0 {
			continue
		}

		if _, err := q.ApplyAccountDelta(ctx, creditdb.ApplyAccountDeltaParams{
			UserID:        p.UserID,
			BalanceDelta:  p.BalanceDelta,
			ReservedDelta: p.ReservedDelta,
		}); err != nil {
			return fmt.Errorf("apply %s to account %s: %w", p.Kind, p.UserID, err)
		}

		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("ledger entry id: %w", err)
		}
		if err := q.InsertLedgerEntry(ctx, creditdb.InsertLedgerEntryParams{
			ID:            id,
			UserID:        p.UserID,
			Kind:          string(p.Kind),
			BalanceDelta:  p.BalanceDelta,
			ReservedDelta: p.ReservedDelta,
			OrderID:       p.OrderID,
			AssignmentID:  p.AssignmentID,
			NowAt:         now,
		}); err != nil {
			return fmt.Errorf("append %s ledger entry for %s: %w", p.Kind, p.UserID, err)
		}
	}

	return nil
}

// Open returns the user's account locked until the transaction ends. A user
// Credit has not seen first receives the starting credits. Concurrent first
// opens wait on the primary key, so the grant is posted once.
func Open(ctx context.Context, q *creditdb.Queries, now time.Time, userID uuid.UUID) (creditdb.Account, error) {
	created, err := q.InsertAccount(ctx, creditdb.InsertAccountParams{UserID: userID, NowAt: now})
	if err != nil {
		return creditdb.Account{}, fmt.Errorf("open account %s: %w", userID, err)
	}

	if created == 1 {
		if err := Post(ctx, q, now, Posting{UserID: userID, Kind: Grant, BalanceDelta: StartingCredits}); err != nil {
			return creditdb.Account{}, err
		}
	}

	a, err := q.LockAccount(ctx, userID)
	if err != nil {
		return creditdb.Account{}, fmt.Errorf("lock account %s: %w", userID, err)
	}
	return a, nil
}

func FromRow(a creditdb.Account) Account {
	return Account{UserID: a.UserID.String(), Balance: a.Balance, Reserved: a.Reserved}
}
