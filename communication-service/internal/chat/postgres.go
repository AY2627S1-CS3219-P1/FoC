package chat

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
	_ Store      = (*PostgresStore)(nil)
	_ OrderStore = (*PostgresStore)(nil)
)

// PostgresStore implements Store and OrderStore with the tables from
// migration 00003.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

func (s *PostgresStore) queries() *db.Queries { return db.New(s.Pool) }

// SaveOrder implements OrderStore.
func (s *PostgresStore) SaveOrder(ctx context.Context, o Order, now time.Time) error {
	if err := s.queries().SaveOrderParticipants(ctx, db.SaveOrderParticipantsParams{
		OrderID:            o.ID,
		RequesterID:        o.RequesterID,
		CourierID:          nullID(o.CourierID),
		Status:             o.Status,
		Revision:           o.Revision,
		PickupLocationName: o.PickupName,
		EndedAt:            nullTime(o.EndedAt),
		UpdatedAt:          now,
	}); err != nil {
		return fmt.Errorf("save order participants: %w", err)
	}
	return nil
}

// GetOrder implements Store.
func (s *PostgresStore) GetOrder(ctx context.Context, id uuid.UUID) (Order, error) {
	row, err := s.queries().GetOrderParticipants(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Order{}, ErrNotFound
	}
	if err != nil {
		return Order{}, fmt.Errorf("get order participants: %w", err)
	}
	return fromRow(db.ListUserOrdersRow(row)), nil
}

// UserOrders implements Store. It filters chat eligibility before applying
// the /chat keyboard limit, using since for the terminal-order grace period.
func (s *PostgresStore) UserOrders(ctx context.Context, userID uuid.UUID, since time.Time) ([]Order, error) {
	rows, err := s.queries().ListUserOrders(ctx, db.ListUserOrdersParams{UserID: userID, Since: since})
	if err != nil {
		return nil, fmt.Errorf("list user orders: %w", err)
	}
	orders := make([]Order, len(rows))
	for i, row := range rows {
		orders[i] = fromRow(row)
	}
	return orders, nil
}

// ReplyTarget implements Store.
func (s *PostgresStore) ReplyTarget(ctx context.Context, chatID, messageID int64) (uuid.UUID, error) {
	orderID, err := s.queries().GetReplyTarget(ctx, db.GetReplyTargetParams{ChatID: chatID, MessageID: messageID})
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
	if err := s.queries().SaveReplyTarget(ctx, db.SaveReplyTargetParams{
		ChatID:    chatID,
		MessageID: messageID,
		OrderID:   orderID,
		CreatedAt: at,
	}); err != nil {
		return fmt.Errorf("save reply target: %w", err)
	}
	return nil
}

// Selection implements Store.
func (s *PostgresStore) Selection(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	orderID, err := s.queries().GetSelection(ctx, userID)
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
	if err := s.queries().SaveSelection(ctx, db.SaveSelectionParams{
		UserID:     userID,
		OrderID:    orderID,
		SelectedAt: at,
	}); err != nil {
		return fmt.Errorf("save selection: %w", err)
	}
	return nil
}

// SaveMessage records an accepted relay attempt once, so retries after an
// ambiguous database response cannot create duplicate dispute records.
func (s *PostgresStore) SaveMessage(ctx context.Context, m Message) error {
	if err := s.queries().SaveChatMessage(ctx, db.SaveChatMessageParams{
		ID:          m.ID,
		OrderID:     m.OrderID,
		SenderID:    m.SenderID,
		RecipientID: m.RecipientID,
		Body:        m.Body,
		SentAt:      m.SentAt,
	}); err != nil {
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
		q := db.New(tx)
		var err error
		if deleted, err = q.DeleteExpiredChatMessages(ctx, cutoff); err != nil {
			return fmt.Errorf("delete chat messages: %w", err)
		}
		if err := q.DeleteExpiredReplyTargets(ctx, cutoff); err != nil {
			return fmt.Errorf("delete reply targets: %w", err)
		}
		if err := q.DeleteExpiredNotificationRecords(ctx, cutoff); err != nil {
			return fmt.Errorf("delete notification records: %w", err)
		}
		if err := q.DeleteClosedSelections(ctx, closedBefore); err != nil {
			return fmt.Errorf("delete closed selections: %w", err)
		}
		// Last, since the deletes above find ended orders through this table.
		if err := q.DeleteExpiredOrderParticipants(ctx, cutoff); err != nil {
			return fmt.Errorf("delete order participants: %w", err)
		}
		return nil
	})
	return deleted, err
}

func fromRow(row db.ListUserOrdersRow) Order {
	o := Order{
		ID:          row.OrderID,
		RequesterID: row.RequesterID,
		Status:      row.Status,
		Revision:    row.Revision,
		PickupName:  row.PickupLocationName,
	}
	if row.CourierID != nil {
		o.CourierID = *row.CourierID
	}
	if row.EndedAt != nil {
		o.EndedAt = *row.EndedAt
	}
	return o
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
