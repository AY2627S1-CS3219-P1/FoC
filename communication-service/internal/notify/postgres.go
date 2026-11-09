package notify

import (
	"context"
	"fmt"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/google/uuid"
)

var _ Deliveries = (*PostgresDeliveries)(nil)

// PostgresDeliveries implements Deliveries with the table from migration 00002.
type PostgresDeliveries struct {
	Queries *db.Queries
}

// HasBeenSent implements Deliveries.
func (d *PostgresDeliveries) HasBeenSent(ctx context.Context, eventID, userID uuid.UUID) (bool, error) {
	sent, err := d.Queries.NotificationSent(ctx, db.NotificationSentParams{EventID: eventID, UserID: userID})
	if err != nil {
		return false, fmt.Errorf("check sent: %w", err)
	}
	return sent, nil
}

// RecordSent implements Deliveries. Recording twice is harmless.
func (d *PostgresDeliveries) RecordSent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error {
	if err := d.Queries.RecordNotificationSent(ctx, db.RecordNotificationSentParams{
		EventID: eventID, UserID: userID, SentAt: at,
	}); err != nil {
		return fmt.Errorf("record sent: %w", err)
	}
	return nil
}

// Start returns when notifications first started in this environment,
// recording now if they never have.
func (d *PostgresDeliveries) Start(ctx context.Context, now time.Time) (time.Time, error) {
	if err := d.Queries.RecordNotificationsStart(ctx, now); err != nil {
		return time.Time{}, fmt.Errorf("record notifications start: %w", err)
	}
	start, err := d.Queries.NotificationsStart(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("get notifications start: %w", err)
	}
	return start, nil
}
