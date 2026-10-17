package chat

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	_ Store      = (*PostgresStore)(nil)
	_ OrderStore = (*PostgresStore)(nil)
)

// PostgresStore implements Store and OrderStore with the tables from
// migration 00003.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// SaveOrder implements OrderStore.
func (s *PostgresStore) SaveOrder(ctx context.Context, o Order, now time.Time) error {
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO order_participants
			(order_id, requester_id, courier_id, status, revision, pickup_location_name, ended_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (order_id) DO UPDATE SET
			requester_id = EXCLUDED.requester_id, courier_id = EXCLUDED.courier_id,
			status = EXCLUDED.status, revision = EXCLUDED.revision,
			pickup_location_name = EXCLUDED.pickup_location_name,
			ended_at = EXCLUDED.ended_at, updated_at = EXCLUDED.updated_at
		WHERE order_participants.revision < EXCLUDED.revision`,
		o.ID, o.RequesterID, nullID(o.CourierID), o.Status, o.Revision, o.PickupName, nullTime(o.EndedAt), now); err != nil {
		return fmt.Errorf("save order participants: %w", err)
	}
	return nil
}

const orderColumns = `order_id, requester_id, courier_id, status, revision, pickup_location_name, ended_at`

func scanOrder(row pgx.Row) (Order, error) {
	var o Order
	var courierID *uuid.UUID
	var endedAt *time.Time
	if err := row.Scan(&o.ID, &o.RequesterID, &courierID, &o.Status, &o.Revision, &o.PickupName, &endedAt); err != nil {
		return Order{}, err
	}
	if courierID != nil {
		o.CourierID = *courierID
	}
	if endedAt != nil {
		o.EndedAt = *endedAt
	}
	return o, nil
}

// GetOrder implements Store.
func (s *PostgresStore) GetOrder(ctx context.Context, id uuid.UUID) (Order, error) {
	o, err := scanOrder(s.Pool.QueryRow(ctx, `SELECT `+orderColumns+` FROM order_participants WHERE order_id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("get order participants: %w", err)
	}
	return o, nil
}

// UserOrders implements Store. It lists orders that haven't ended or ended
// after since, capped for the /chat keyboard.
func (s *PostgresStore) UserOrders(ctx context.Context, userID uuid.UUID, since time.Time) ([]Order, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+orderColumns+` FROM order_participants
		WHERE (requester_id = $1 OR courier_id = $1) AND (ended_at IS NULL OR ended_at > $2)
		ORDER BY updated_at DESC LIMIT 20`, userID, since)
	if err != nil {
		return nil, fmt.Errorf("list user orders: %w", err)
	}
	defer rows.Close()
	var orders []Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, fmt.Errorf("scan user order: %w", err)
		}
		orders = append(orders, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list user orders: %w", err)
	}
	return orders, nil
}

// ReplyTarget implements Store.
func (s *PostgresStore) ReplyTarget(ctx context.Context, chatID, messageID int64) (uuid.UUID, error) {
	var orderID uuid.UUID
	err := s.Pool.QueryRow(ctx,
		`SELECT order_id FROM chat_reply_targets WHERE chat_id = $1 AND message_id = $2`, chatID, messageID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get reply target: %w", err)
	}
	return orderID, nil
}

// SaveReplyTarget implements Store.
func (s *PostgresStore) SaveReplyTarget(ctx context.Context, chatID, messageID int64, orderID uuid.UUID, at time.Time) error {
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO chat_reply_targets (chat_id, message_id, order_id, created_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (chat_id, message_id) DO UPDATE SET order_id = EXCLUDED.order_id, created_at = EXCLUDED.created_at`,
		chatID, messageID, orderID, at); err != nil {
		return fmt.Errorf("save reply target: %w", err)
	}
	return nil
}

// Selection implements Store.
func (s *PostgresStore) Selection(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var orderID uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT order_id FROM chat_selections WHERE user_id = $1`, userID).Scan(&orderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("get selection: %w", err)
	}
	return orderID, nil
}

// Select implements Store.
func (s *PostgresStore) Select(ctx context.Context, userID, orderID uuid.UUID, at time.Time) error {
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO chat_selections (user_id, order_id, selected_at) VALUES ($1, $2, $3)
		ON CONFLICT (user_id) DO UPDATE SET order_id = EXCLUDED.order_id, selected_at = EXCLUDED.selected_at`,
		userID, orderID, at); err != nil {
		return fmt.Errorf("save selection: %w", err)
	}
	return nil
}

// SaveMessage implements Store.
func (s *PostgresStore) SaveMessage(ctx context.Context, m Message) error {
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO chat_messages (id, order_id, sender_id, recipient_id, body, sent_at) VALUES ($1, $2, $3, $4, $5, $6)`,
		m.ID, m.OrderID, m.SenderID, m.RecipientID, m.Body, m.SentAt); err != nil {
		return fmt.Errorf("save chat message: %w", err)
	}
	return nil
}

// DeleteExpired removes chat data for orders that ended before cutoff,
// selections of orders whose chat has closed, and notification records older
// than cutoff. It returns how many chat messages were deleted.
func (s *PostgresStore) DeleteExpired(ctx context.Context, cutoff, closedBefore time.Time) (int64, error) {
	var deleted int64
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			DELETE FROM chat_messages m USING order_participants p
			WHERE m.order_id = p.order_id AND p.ended_at < $1`, cutoff)
		if err != nil {
			return fmt.Errorf("delete chat messages: %w", err)
		}
		deleted = tag.RowsAffected()
		for _, q := range []string{
			`DELETE FROM chat_reply_targets t USING order_participants p
			 WHERE t.order_id = p.order_id AND p.ended_at < $1`,
			// Notifications to orders never seen here still expire by age.
			`DELETE FROM chat_reply_targets WHERE created_at < $1`,
			`DELETE FROM order_notifications_sent WHERE sent_at < $1`,
			`DELETE FROM order_participants WHERE ended_at < $1`,
		} {
			if _, err := tx.Exec(ctx, q, cutoff); err != nil {
				return fmt.Errorf("delete expired chat data: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM chat_selections s USING order_participants p
			WHERE s.order_id = p.order_id AND p.ended_at < $1`, closedBefore); err != nil {
			return fmt.Errorf("delete closed selections: %w", err)
		}
		return nil
	})
	return deleted, err
}

func nullID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

func nullTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
