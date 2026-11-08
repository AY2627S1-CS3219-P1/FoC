package account

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/database/creditdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) Account(ctx context.Context, userID string) (Account, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Account{}, ErrInvalidArgument
	}

	a, err := creditdb.New(s.pool).GetAccount(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Account{}, ErrAccountNotFound
	}
	if err != nil {
		return Account{}, fmt.Errorf("get account %s: %w", id, err)
	}
	return FromRow(a), nil
}

func (s *PostgresStore) LedgerEntries(
	ctx context.Context,
	userID string,
	limit, offset int64,
) ([]LedgerEntry, int64, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, ErrInvalidArgument
	}
	q := creditdb.New(s.pool)
	rows, err := q.ListLedgerEntries(
		ctx,
		creditdb.ListLedgerEntriesParams{UserID: id, PageSize: limit, PageOffset: offset},
	)
	if err != nil {
		return nil, 0, err
	}
	total, err := q.CountLedgerEntries(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	out := make([]LedgerEntry, len(rows))
	for i, r := range rows {
		out[i] = LedgerEntry{
			ID: r.ID.String(), Kind: Kind(r.Kind), BalanceDelta: r.BalanceDelta, ReservedDelta: r.ReservedDelta,
			OrderID: idString(r.OrderID), AssignmentID: idString(r.AssignmentID), CreatedAt: r.CreatedAt.UTC(),
		}
	}
	return out, total, nil
}

// Open opens the account in its own transaction. account.Open grants the
// starting credits only to an account it creates, and never for a user whose
// committed deletion Credit has recorded.
func (s *PostgresStore) Open(ctx context.Context, userID string, at time.Time) error {
	id, err := uuid.Parse(userID)
	if err != nil {
		return ErrInvalidArgument
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin open account: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	q := creditdb.New(tx)
	if err := q.LockUser(ctx, id); err != nil {
		return fmt.Errorf("lock user %s: %w", id, err)
	}

	// A delayed users.create after users.user_deleted must not reopen the
	// account or grant credits.
	marked, err := DeletionMarked(ctx, q, id)
	if err != nil {
		return err
	}
	if marked {
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit open account %s: %w", id, err)
		}
		return nil
	}

	if _, err := Open(ctx, q, at, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit open account %s: %w", id, err)
	}
	return nil
}

// RecordDeletion stores the users.user_deleted marker in its own transaction,
// under the same per-user lock the opening paths take. A redelivered event, or
// a second fact for the same user, changes nothing.
func (s *PostgresStore) RecordDeletion(ctx context.Context, userID, eventID uuid.UUID, deletedAt time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin record deletion: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck

	q := creditdb.New(tx)
	if err := q.LockUser(ctx, userID); err != nil {
		return fmt.Errorf("lock user %s: %w", userID, err)
	}
	if _, err := q.InsertDeletedUser(ctx, creditdb.InsertDeletedUserParams{
		UserID:    userID,
		EventID:   eventID,
		DeletedAt: deletedAt,
	}); err != nil {
		return fmt.Errorf("record deleted user %s: %w", userID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit record deletion %s: %w", userID, err)
	}
	return nil
}

func idString(v *uuid.UUID) string {
	if v == nil {
		return ""
	}
	return v.String()
}

var (
	_ Store            = (*PostgresStore)(nil)
	_ Opener           = (*PostgresStore)(nil)
	_ DeletionRecorder = (*PostgresStore)(nil)
)
