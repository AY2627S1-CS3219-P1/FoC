package notify

import (
	"context"
	"fmt"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
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
	sent, err := db.New(d.Pool).NotificationSent(ctx, db.NotificationSentParams{EventID: eventID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("check sent: %w", err)
	}
	return sent, nil
}

// RecordSent implements Deliveries. Recording twice is harmless.
func (d *PostgresDeliveries) RecordSent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error {
	if err := db.New(d.Pool).RecordNotificationSent(ctx, db.RecordNotificationSentParams{
		EventID: eventID, UserID: userID, SentAt: at,
	}); err != nil {
		return fmt.Errorf("record sent: %w", err)
	}
	return nil
}
