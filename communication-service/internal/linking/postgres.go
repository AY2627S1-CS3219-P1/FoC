package linking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var _ Store = (*PostgresStore)(nil)

// PostgresStore implements Store with the tables from migration 00001.
type PostgresStore struct {
	Pool *pgxpool.Pool
}

// ReplaceCode implements Store.
func (s *PostgresStore) ReplaceCode(ctx context.Context, userID uuid.UUID, codeHash []byte, expiresAt, now time.Time) error {
	return pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`DELETE FROM telegram_link_codes WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
			return fmt.Errorf("delete unused codes: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO telegram_link_codes (code_hash, user_id, expires_at, created_at) VALUES ($1, $2, $3, $4)`,
			codeHash, userID, expiresAt, now); err != nil {
			return fmt.Errorf("insert code: %w", err)
		}
		return nil
	})
}

// RedeemCode implements Store.
func (s *PostgresStore) RedeemCode(ctx context.Context, codeHash []byte, chat Chat, now time.Time) (Link, error) {
	var link Link
	err := pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// Locking the code row serializes concurrent redemptions of it.
		var userID uuid.UUID
		err := tx.QueryRow(ctx, `
			UPDATE telegram_link_codes SET used_at = $2
			WHERE code_hash = $1 AND used_at IS NULL AND expires_at > $2
			RETURNING user_id`, codeHash, now).Scan(&userID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrInvalidCode
		}
		if err != nil {
			return fmt.Errorf("use code: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM telegram_links WHERE chat_id = $1 AND user_id <> $2`, chat.ID, userID); err != nil {
			return fmt.Errorf("release chat: %w", err)
		}
		link = Link{UserID: userID, ChatID: chat.ID, Username: chat.Username, LinkedAt: now}
		if _, err := tx.Exec(ctx, `
			INSERT INTO telegram_links (user_id, chat_id, telegram_username, linked_at)
			VALUES ($1, $2, NULLIF($3, ''), $4)
			ON CONFLICT (user_id) DO UPDATE
			SET chat_id = EXCLUDED.chat_id, telegram_username = EXCLUDED.telegram_username, linked_at = EXCLUDED.linked_at`,
			link.UserID, link.ChatID, link.Username, link.LinkedAt); err != nil {
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
	link := Link{UserID: userID}
	var username *string
	err := s.Pool.QueryRow(ctx,
		`SELECT chat_id, telegram_username, linked_at FROM telegram_links WHERE user_id = $1`, userID).
		Scan(&link.ChatID, &username, &link.LinkedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNotLinked
	}
	if err != nil {
		return Link{}, fmt.Errorf("get link: %w", err)
	}
	if username != nil {
		link.Username = *username
	}
	return link, nil
}

// DeleteLink implements Store.
func (s *PostgresStore) DeleteLink(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.Pool.Exec(ctx, `DELETE FROM telegram_links WHERE user_id = $1`, userID); err != nil {
		return fmt.Errorf("delete link: %w", err)
	}
	return nil
}

// DeleteLinkByChat implements Store.
func (s *PostgresStore) DeleteLinkByChat(ctx context.Context, chatID int64) error {
	if _, err := s.Pool.Exec(ctx, `DELETE FROM telegram_links WHERE chat_id = $1`, chatID); err != nil {
		return fmt.Errorf("delete link by chat: %w", err)
	}
	return nil
}
