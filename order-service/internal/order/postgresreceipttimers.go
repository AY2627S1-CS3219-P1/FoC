package order

import (
	"context"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
	"github.com/google/uuid"
)

func (r *PostgresRepository) WithinReceiptTimers(ctx context.Context, fn func(ReceiptTimerTx) error) error {
	return r.WithinProofs(ctx, func(tx ProofTx) error { return fn(tx) })
}

// DueReceiptTimers lists delivered orders whose receipt window has passed.
func (r *PostgresRepository) DueReceiptTimers(
	ctx context.Context,
	now time.Time,
	after string,
	limit int32,
) ([]string, error) {
	cursor, err := uuid.Parse(after)
	if err != nil || limit < 1 || limit > receiptTimerBatchSize {
		return nil, ErrInvalid
	}

	rows, err := db.New(r.pool).DueReceiptTimers(ctx, db.DueReceiptTimersParams{
		DeliveredBefore: now.Add(-ReceiptWindow),
		AfterID:         cursor,
		BatchSize:       limit,
	})
	if err != nil {
		return nil, persistenceError(err)
	}

	ids := make([]string, len(rows))
	for i, id := range rows {
		ids[i] = id.String()
	}
	return ids, nil
}

var _ ReceiptTimerRepository = (*PostgresRepository)(nil)
