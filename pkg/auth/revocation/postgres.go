package revocation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// connectTimeout bounds dialing a new pool connection so an unreachable
	// store fails fast instead of stalling startup or request handling.
	connectTimeout = 3 * time.Second

	// statementTimeout bounds any single query so a stuck backend cannot hang
	// an authenticated request; revocation lookups are single-index probes,
	// far below it.
	statementTimeout = 5 * time.Second

	// revokedAccessTokenQuery checks if a session's access tokens have been revoked.
	// The expires_at > now() predicate ensures expired entries are invisible, so
	// entries need no active deletion to stop matching reads.
	revokedAccessTokenQuery = `SELECT 1 FROM revoked_access_tokens WHERE sid = $1 AND expires_at > now() LIMIT 1`

	// insertRevocationQuery durably records a fresh revocation. now()+ttl is the expiry.
	// ON CONFLICT (sid) DO UPDATE makes re-revoking idempotent and refreshes the expiry.
	insertRevocationQuery = `INSERT INTO revoked_access_tokens (sid, expires_at)
		VALUES ($1, now() + $2) ON CONFLICT (sid) DO UPDATE SET expires_at = EXCLUDED.expires_at`

	// deleteExpiredQuery removes rows where expires_at <= now(), bounding the
	// table and index size. The read predicate already filters expired entries,
	// so this only bounds stale data, not correctness.
	deleteExpiredQuery = `DELETE FROM revoked_access_tokens WHERE expires_at <= now()`
)

// postgresStore is the PostgreSQL-backed revocation implementation.
type postgresStore struct {
	pool *pgxpool.Pool
	ttl  time.Duration
}

// newPostgresStore creates a PostgreSQL connection pool with a statement timeout
// and verifies the connection eagerly.
func newPostgresStore(ctx context.Context, dsn string, ttl time.Duration) (*postgresStore, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}

	cfg.MaxConns = 5 // small pool: only revocation queries, not load-bearing

	// Fail fast on dial and per-statement execution.
	cfg.ConnConfig.ConnectTimeout = connectTimeout
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = statementTimeout.String()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	// Ensure the schema exists; idempotent.
	if err := ensureSchema(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ensure schema: %w", err)
	}

	// Eager ping so a bad DSN fails startup, not on first request.
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	return &postgresStore{pool: pool, ttl: ttl}, nil
}

// ensureSchema creates the revoked_access_tokens table if it doesn't exist.
// It is idempotent and safe to call on every connection.
func ensureSchema(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS revoked_access_tokens (
			sid        TEXT PRIMARY KEY,
			revoked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			expires_at TIMESTAMPTZ NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	// Create index for cleanup only; readers use the primary key lookup.
	_, err = pool.Exec(ctx, `
		CREATE INDEX IF NOT EXISTS revoked_access_tokens_expires_at_idx
			ON revoked_access_tokens (expires_at)
	`)
	if err != nil {
		return fmt.Errorf("create index: %w", err)
	}

	return nil
}

// Revoke durably records fresh revocations and returns only after the commit.
// All inserts run in one transaction: either every session is recorded or none
// is, so a failure cannot leave a multi-session logout-all half-applied.
func (p *postgresStore) Revoke(ctx context.Context, sessionIDs ...string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	if p.ttl <= 0 {
		return fmt.Errorf("revocation store is not configured for writes")
	}

	batch := &pgx.Batch{}
	for _, sid := range sessionIDs {
		batch.Queue(insertRevocationQuery, sid, p.ttl)
	}
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin revocation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tx.SendBatch(ctx, batch).Close(); err != nil {
		return fmt.Errorf("record revoked access tokens: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit revoked access tokens: %w", err)
	}
	return nil
}

// IsRevoked issues one indexed lookup via primary key and treats no row as not revoked.
func (p *postgresStore) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}

	var found int
	err := p.pool.QueryRow(ctx, revokedAccessTokenQuery, sessionID).Scan(&found)
	// pgx returns its own ErrNoRows which wraps database/sql.ErrNoRows, so the
	// comparison must go through errors.Is — identity (==) never matches.
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check postgres revocation for session %q: %w", sessionID, err)
	}

	return true, nil
}

// DeleteExpired removes rows where expires_at <= now() and returns the count of
// deleted rows. Called by the User Service on startup and periodically to bound
// the table/index size.
func (p *postgresStore) DeleteExpired(ctx context.Context) (int64, error) {
	result, err := p.pool.Exec(ctx, deleteExpiredQuery)
	if err != nil {
		return 0, fmt.Errorf("delete expired revocations: %w", err)
	}
	return result.RowsAffected(), nil
}

// Close releases the PostgreSQL connection pool.
func (p *postgresStore) Close() error {
	p.pool.Close()
	return nil
}
