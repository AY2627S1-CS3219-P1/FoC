package linking

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

var _ Store = (*PostgresStore)(nil)

// PostgresStore implements Store with the tables from migration 00001.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

func (s *PostgresStore) queries() *db.Queries { return db.New(s.Pool) }

// ReplaceCode implements Store. The partial unique index on unused codes
// makes concurrent calls for one user leave a single unused code.
func (s *PostgresStore) ReplaceCode(ctx context.Context, userID uuid.UUID, codeHash []byte, expiresAt, now time.Time) error {
	if err := s.queries().ReplaceCode(ctx, db.ReplaceCodeParams{
		CodeHash:  codeHash,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}); err != nil {
		return fmt.Errorf("save code: %w", err)
	}
	return nil
}

// RedeemCode implements Store.
func (s *PostgresStore) RedeemCode(ctx context.Context, codeHash []byte, chat Chat, now time.Time) (Link, error) {
	var link Link
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Serialize links for one chat, so concurrent redemptions from the same
		// chat move it to the last one instead of failing on chat_id.
		if err := q.LockChat(ctx, chat.ID); err != nil {
			return fmt.Errorf("lock chat: %w", err)
		}
		userID, err := q.UseCode(ctx, db.UseCodeParams{Now: now, CodeHash: codeHash})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return fmt.Errorf("use code: %w", err)
		}
		if err := q.ReleaseChat(ctx, db.ReleaseChatParams{ChatID: chat.ID, UserID: userID}); err != nil {
			return fmt.Errorf("release chat: %w", err)
		}
		link = Link{UserID: userID, ChatID: chat.ID, Username: chat.Username, LinkedAt: now}
		if err := q.SaveLink(ctx, db.SaveLinkParams{
			UserID:           link.UserID,
			ChatID:           link.ChatID,
			TelegramUsername: optional(link.Username),
			LinkedAt:         link.LinkedAt,
		}); err != nil {
			return fmt.Errorf("save link: %w", err)
		}
		return nil
	})
	if err != nil {
		return Link{}, err
	}
	return link, nil
}

// GetLink implements Store.
func (s *PostgresStore) GetLink(ctx context.Context, userID uuid.UUID) (Link, error) {
	row, err := s.queries().GetLink(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotLinked
	}
	if err != nil {
		return Link{}, fmt.Errorf("get link: %w", err)
	}
	return fromRow(row), nil
}

// DeleteLink implements Store. It also deletes the user's unused codes, so an
// earlier link can't reconnect the account after they disconnect.
func (s *PostgresStore) DeleteLink(ctx context.Context, userID uuid.UUID) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := q.DeleteUnusedCodes(ctx, userID); err != nil {
			return fmt.Errorf("delete unused codes: %w", err)
		}
		if err := q.DeleteLink(ctx, userID); err != nil {
			return fmt.Errorf("delete link: %w", err)
		}
		return nil
	})
}

// DeleteLinkByChat implements Store.
func (s *PostgresStore) DeleteLinkByChat(ctx context.Context, chatID int64) error {
	if err := s.queries().DeleteLinkByChat(ctx, chatID); err != nil {
		return fmt.Errorf("delete link by chat: %w", err)
	}
	return nil
}

func fromRow(row db.TelegramLink) Link {
	link := Link{UserID: row.UserID, ChatID: row.ChatID, LinkedAt: row.LinkedAt}
	if row.TelegramUsername != nil {
		link.Username = *row.TelegramUsername
	}
	return link
}

// optional stores an empty Telegram username as NULL.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
