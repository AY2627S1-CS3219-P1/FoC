package infra

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// EnsureSchema makes the revoked_access_tokens table match what the write-aware
// benchmark needs: an expires_at column (defaulting to now()) and an index that
// supports both the read predicate `expires_at > now()` and a reaper that
// removes expired rows. It is idempotent — ALTER … IF NOT EXISTS / CREATE INDEX
// IF NOT EXISTS — so it is safe on fresh volumes and on the benchmark's own
// repeated runs. The benchmark owns and TRUNCATEs this table, so no migration
// framework is involved.
func EnsureSchema(ctx context.Context, dsn string) error {
	raw, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer func() { _ = raw.Close(ctx) }()

	queries := []string{
		`ALTER TABLE revoked_access_tokens
			ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NOT NULL DEFAULT now()`,
		`CREATE INDEX IF NOT EXISTS revoked_access_tokens_expires_at_idx
			ON revoked_access_tokens (expires_at)`,
	}
	for _, q := range queries {
		if _, err := raw.Exec(ctx, q); err != nil {
			return fmt.Errorf("ensure schema: %w", err)
		}
	}

	return nil
}
