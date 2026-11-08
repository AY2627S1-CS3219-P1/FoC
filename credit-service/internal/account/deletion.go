package account

import (
	"context"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DeletionMarked reports whether Credit has recorded the committed deletion of
// the user's FoC account. Call it while holding the user's advisory lock, in
// the transaction that opens the account or grants its starting credits, so a
// delayed users.create can never re-provision a deleted user.
func DeletionMarked(ctx context.Context, q *creditdb.Queries, userID uuid.UUID) (bool, error) {
	_, err := q.GetDeletedUser(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check deleted user %s: %w", userID, err)
	}
	return true, nil
}
