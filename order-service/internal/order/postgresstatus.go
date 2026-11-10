package order

import (
	"context"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

func (r *PostgresRepository) AssignmentStatus(ctx context.Context, id string) (AssignmentAttempt, error) {
	row, err := db.New(r.pool).GetAssignmentAttempt(ctx, uuid.MustParse(id))
	return fromAssignment(row), persistenceError(err)
}

func (r *PostgresRepository) CancellationStatus(ctx context.Context, id string) (Cancellation, error) {
	row, err := db.New(r.pool).GetCancellation(ctx, uuid.MustParse(id))
	return fromCancellation(row), persistenceError(err)
}
