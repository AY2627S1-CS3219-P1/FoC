package notify

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ Deliveries = (*PostgresDeliveries)(nil)

// PostgresDeliveries implements Deliveries with the table from migration 00002.
type PostgresDeliveries struct {
	Pool *pgxpool.Pool
}

// Sent implements Deliveries.
func (d *PostgresDeliveries) Sent(ctx context.Context, eventID, userID uuid.UUID) (bool, error) {
	var sent bool
	err := d.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM order_notifications_sent WHERE event_id = $1 AND user_id = $2)`,
		eventID, userID).Scan(&sent)
	if err != nil {
		return false, fmt.Errorf("check sent: %w", err)
	}
	return sent, nil
}

// RecordSent implements Deliveries. Recording twice is harmless.
func (d *PostgresDeliveries) RecordSent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error {
	if _, err := d.Pool.Exec(ctx, `
		INSERT INTO order_notifications_sent (event_id, user_id, sent_at) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, eventID, userID, at); err != nil {
		return fmt.Errorf("record sent: %w", err)
	}
	return nil
}
