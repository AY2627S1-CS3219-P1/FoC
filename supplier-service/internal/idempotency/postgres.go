package idempotency

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/idempotencydb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// PostgresStore uses its caller's transaction for the lock, record, and
// resource writes. The advisory lock remains held until that transaction ends.
type PostgresStore struct{ queries *db.Queries }

func NewPostgresStore(tx pgx.Tx) *PostgresStore {
	return &PostgresStore{queries: db.New(tx)}
}

func (s *PostgresStore) Lock(ctx context.Context, scope Scope) error {
	// JSON preserves the old structured scope and avoids delimiter collisions.
	encoded, err := json.Marshal(scope)
	if err != nil {
		return fmt.Errorf("encode idempotency scope: %w", err)
	}
	if err := s.queries.LockIdempotency(ctx, string(encoded)); err != nil {
		return fmt.Errorf("lock idempotency scope: %w", err)
	}
	return nil
}

func (s *PostgresStore) Find(ctx context.Context, scope Scope, now time.Time) (*Record, error) {
	key, err := uuid.Parse(scope.Key)
	if err != nil {
		return nil, fmt.Errorf("parse idempotency key: %w", err)
	}
	if err := s.queries.DeleteExpiredIdempotency(ctx, db.DeleteExpiredIdempotencyParams{
		CallerID: scope.Caller, Method: scope.Method, Key: key, NowAt: now.UTC(),
	}); err != nil {
		return nil, fmt.Errorf("delete expired idempotency record: %w", err)
	}
	value, err := s.queries.GetIdempotency(ctx, db.GetIdempotencyParams{
		CallerID: scope.Caller, Method: scope.Method, Key: key,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find idempotency record: %w", err)
	}
	return &Record{Hash: value.RequestHash, ResourceID: value.ResourceID.String(), ExpiresAt: value.ExpiresAt.UTC()}, nil
}

func (s *PostgresStore) Save(ctx context.Context, scope Scope, value Record) error {
	key, err := uuid.Parse(scope.Key)
	if err != nil {
		return fmt.Errorf("parse idempotency key: %w", err)
	}
	resourceID, err := uuid.Parse(value.ResourceID)
	if err != nil {
		return fmt.Errorf("parse idempotency resource ID: %w", err)
	}
	if err := s.queries.InsertIdempotency(ctx, db.InsertIdempotencyParams{
		CallerID: scope.Caller, Method: scope.Method, Key: key,
		RequestHash: value.Hash, ResourceID: resourceID, ExpiresAt: value.ExpiresAt.UTC(),
	}); err != nil {
		return fmt.Errorf("save idempotency record: %w", err)
	}
	return nil
}

var _ Store = (*PostgresStore)(nil)
