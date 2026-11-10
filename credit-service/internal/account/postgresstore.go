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
// starting credits only to an account it creates.
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

	if _, err := Open(ctx, creditdb.New(tx), at, id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit open account %s: %w", id, err)
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
	_ Store  = (*PostgresStore)(nil)
	_ Opener = (*PostgresStore)(nil)
)

// Totals reads credit totals across all accounts from one snapshot.
func (s *PostgresStore) Totals(ctx context.Context) (Totals, error) {
	row, err := creditdb.New(s.pool).GetCreditTotals(ctx)
	if err != nil {
		return Totals{}, fmt.Errorf("get credit totals: %w", err)
	}
	return Totals{Reserved: row.Reserved, Returned: row.Returned, Transferred: row.Transferred}, nil
}

// DailyPayouts sums courier payouts in [from, to) for each Asia/Singapore day
// that has any.
func (s *PostgresStore) DailyPayouts(ctx context.Context, from, to time.Time) (map[Date]DailyVolume, error) {
	rows, err := creditdb.New(s.pool).DailyPayouts(ctx, creditdb.DailyPayoutsParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, fmt.Errorf("get daily payouts: %w", err)
	}

	volumes := make(map[Date]DailyVolume, len(rows))
	for _, row := range rows {
		if !row.Day.Valid {
			return nil, errors.New("daily payout day is null")
		}
		day := Date{Year: row.Day.Time.Year(), Month: row.Day.Time.Month(), Day: row.Day.Time.Day()}
		volumes[day] = DailyVolume{Date: day, Credits: row.Credits, Payments: row.Payments}
	}
	return volumes, nil
}
