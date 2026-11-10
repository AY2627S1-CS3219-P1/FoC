package notify

import (
	"context"
	"errors"
	"fmt"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	_ EmailQueue    = (*PostgresEmailQueue)(nil)
	_ PendingEmails = (*PostgresEmailQueue)(nil)
)

// PostgresEmailQueue owns durable fallback acceptance and transactional claims.
type PostgresEmailQueue struct {
	Pool *pgxpool.Pool
}

// Enqueue commits before the notification handler acknowledges Kafka. Replays
// preserve the first message and cannot requeue a completed delivery.
func (q *PostgresEmailQueue) Enqueue(
	ctx context.Context,
	eventID, userID uuid.UUID,
	text string,
	at time.Time,
	validity EmailValidity,
) error {
	var expiresAt *time.Time
	if !validity.ExpiresAt.IsZero() {
		expiresAt = &validity.ExpiresAt
	}
	var orderID *uuid.UUID
	var revision *int64
	if validity.OrderID != uuid.Nil {
		orderID, revision = &validity.OrderID, &validity.Revision
	}
	if err := db.New(q.Pool).EnqueueNotificationEmail(ctx, db.EnqueueNotificationEmailParams{
		EventID: eventID, UserID: userID, Body: text, CreatedAt: at,
		ExpiresAt: expiresAt, OrderID: orderID, OrderRevision: revision, UnlessReassigned: validity.UnlessReassigned,
	}); err != nil {
		return fmt.Errorf("enqueue email: %w", err)
	}
	return nil
}

// WithNext locks one ready row through the SMTP attempt. SKIP LOCKED lets
// concurrent workers progress without leasing a row that might still be sent.
// Success records delivery and removes the queued row in the same commit.
func (q *PostgresEmailQueue) WithNext(
	ctx context.Context,
	at time.Time,
	send func(EmailDelivery) (EmailResult, error),
) (bool, error) {
	processed := false
	err := pgx.BeginFunc(ctx, q.Pool, func(tx pgx.Tx) error {
		queries := db.New(tx)
		row, err := queries.NextNotificationEmail(ctx, db.NextNotificationEmailParams{
			Now:        at,
			FreshAfter: at.Add(-LateWindow),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("claim email: %w", err)
		}
		processed = true
		// A replay INSERT can wait behind completion's DELETE and then insert
		// using its older sent-record snapshot. This separate statement runs
		// after the row lock is acquired and sees the committed completion.
		sent, err := queries.NotificationSent(ctx, db.NotificationSentParams{EventID: row.EventID, UserID: row.UserID})
		if err != nil {
			return fmt.Errorf("check completed email: %w", err)
		}
		if sent {
			return queries.DeleteNotificationEmail(ctx, db.DeleteNotificationEmailParams{
				EventID: row.EventID, UserID: row.UserID,
			})
		}
		if row.SkipReason != "" {
			return queries.SkipNotificationEmail(ctx, db.SkipNotificationEmailParams{
				EventID: row.EventID, UserID: row.UserID, AttemptedAt: &at, LastError: row.SkipReason,
			})
		}
		result, err := send(EmailDelivery{
			EventID: row.EventID, UserID: row.UserID, Address: row.Email, Text: row.Body,
		})
		if err != nil {
			return err
		}

		switch {
		case !result.RetryAt.IsZero():
			return queries.RetryNotificationEmail(ctx, db.RetryNotificationEmailParams{
				EventID: row.EventID, UserID: row.UserID, AvailableAt: result.RetryAt,
				AttemptedAt: &at, LastError: result.Error,
			})
		case result.Error != "":
			return queries.RejectNotificationEmail(ctx, db.RejectNotificationEmailParams{
				EventID: row.EventID, UserID: row.UserID, AttemptedAt: &at, LastError: result.Error,
			})
		default:
			if err := queries.RecordNotificationSent(ctx, db.RecordNotificationSentParams{
				EventID: row.EventID, UserID: row.UserID, SentAt: at,
			}); err != nil {
				return fmt.Errorf("record email sent: %w", err)
			}
			return queries.DeleteNotificationEmail(ctx, db.DeleteNotificationEmailParams{
				EventID: row.EventID, UserID: row.UserID,
			})
		}
	})
	return processed, err
}
