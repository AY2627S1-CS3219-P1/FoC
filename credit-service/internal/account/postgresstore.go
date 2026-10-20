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

type PostgresStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

func NewPostgresStore(pool *pgxpool.Pool, clock func() time.Time) *PostgresStore {
	return &PostgresStore{pool: pool, clock: clock}
}

func (s *PostgresStore) Account(ctx context.Context, userID string) (Account, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return Account{}, ErrInvalidArgument
	}
	a, err := creditdb.New(s.pool).GetAccount(ctx, id)
	if err == nil {
		return FromRow(a), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Account{}, fmt.Errorf("get account %s: %w", id, err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Account{}, fmt.Errorf("begin account open: %w", err)
	}
	defer tx.Rollback(context.WithoutCancel(ctx)) //nolint:errcheck
	a, err = Open(ctx, creditdb.New(tx), s.clock().UTC(), id)
	if err != nil {
		return Account{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Account{}, fmt.Errorf("commit account open: %w", err)
	}
	return FromRow(a), nil
}

func (s *PostgresStore) LedgerEntries(ctx context.Context, userID string, limit, offset int64) ([]LedgerEntry, int64, error) {
	id, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, ErrInvalidArgument
	}
	q := creditdb.New(s.pool)
	rows, err := q.ListLedgerEntries(ctx, creditdb.ListLedgerEntriesParams{UserID: id, PageSize: limit, PageOffset: offset})
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

func idString(v *uuid.UUID) string {
	if v == nil {
		return ""
	}
	return v.String()
}

var _ Store = (*PostgresStore)(nil)
