package account

import (
	"context"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/google/uuid"
)

// OutstandingWork reads every drain predicate in one statement, so the
// account, reservations and assignments are always observed on one snapshot.
// The read never creates an account.
func (s *PostgresStore) OutstandingWork(ctx context.Context, userID string) (OutstandingWork, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return OutstandingWork{}, ErrInvalidArgument
	}

	row, err := creditdb.New(s.pool).GetUserOutstandingWork(ctx, id)
	if err != nil {
		return OutstandingWork{}, fmt.Errorf("outstanding work %s: %w", id, err)
	}

	var out OutstandingWork
	if row.ReservedBalance {
		out.Reasons = append(out.Reasons, OutstandingReservedBalance)
	}
	if row.ActiveReservation {
		out.Reasons = append(out.Reasons, OutstandingActiveReservation)
	}
	if row.UnsettledAssignment {
		out.Reasons = append(out.Reasons, OutstandingUnsettledAssignment)
	}
	return out, nil
}

var _ DrainReader = (*PostgresStore)(nil)

