package checker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// revokedAccessTokenQuery matches a present, not-yet-expired revocation. The
	// expires_at > now() predicate mirrors Redis's implicit TTL expiry: a key
	// that has expired is invisible to the check, exactly as it would be once the
	// Redis entry lapses.
	revokedAccessTokenQuery = `SELECT 1 FROM revoked_access_tokens WHERE sid = $1 AND expires_at > now() LIMIT 1`
	// insertRevocationQuery durably records one fresh revocation. now()+ttl is
	// the expiry, mirroring Redis SET … EX. ON CONFLICT DO NOTHING keeps a
	// stray reuse of an ID from failing the durable write.
	insertRevocationQuery = `INSERT INTO revoked_access_tokens (sid, expires_at)
		VALUES ($1, now() + $2) ON CONFLICT (sid) DO NOTHING`
	// deleteQuery removes a batch of write keys to reset the population.
	deleteQuery = `DELETE FROM revoked_access_tokens WHERE sid = ANY($1)`

	// statementTimeout bounds any single query so a stuck backend cannot hang
	// the load generator; durable writes here are far below it.
	statementTimeout = 30 * time.Second
)

// PG checks revocation against the Postgres revoked_access_tokens table.
type PG struct {
	pool *pgxpool.Pool
}

// NewPG creates a pool capped at maxConns (the benchmark's intentional
// concurrency knob: excess work queues on the real connections) with a
// statement timeout as a safety net.
func NewPG(dsn string, maxConns int) (*PG, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}

	cfg.MaxConns = int32(maxConns)
	setStatementTimeout(cfg.ConnConfig, statementTimeout)

	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}

	return &PG{pool: pool}, nil
}

// IsRevoked issues one indexed lookup and treats pgx.ErrNoRows as not revoked.
func (p *PG) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	var found int
	err := p.pool.QueryRow(ctx, revokedAccessTokenQuery, sessionID).Scan(&found)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("check postgres revocation for session %q: %w", sessionID, err)
	}

	return true, nil
}

// Revoke durably inserts one fresh revocation (a committed transaction, since
// synchronous_commit is on) and returns only after the ack. sessionID must be a
// unique ID the benchmark owns.
func (p *PG) Revoke(ctx context.Context, sessionID string, ttl time.Duration) error {
	if _, err := p.pool.Exec(ctx, insertRevocationQuery, sessionID, ttl); err != nil {
		return fmt.Errorf("revoke postgres session %q: %w", sessionID, err)
	}

	return nil
}

// Delete removes the given keys in one statement, resetting the write-key
// population between measured windows.
func (p *PG) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	if _, err := p.pool.Exec(ctx, deleteQuery, ids); err != nil {
		return fmt.Errorf("delete postgres sessions: %w", err)
	}

	return nil
}

// PoolStats returns a snapshot of the pool's waits, used to report
// connection-pool queueing. Implements checker.StatProvider.
func (p *PG) PoolStats() *PoolStat {
	return PoolStatFrom(p.pool.Stat())
}

// Close releases the pool's connections.
func (p *PG) Close() error {
	p.pool.Close()

	return nil
}

// setStatementTimeout caps any single query on the pool so a stuck backend
// cannot hang the load generator. In pgx v5 this is a per-connection session
// default set via the connection's runtime params.
func setStatementTimeout(connCfg *pgx.ConnConfig, d time.Duration) {
	if connCfg == nil {
		return
	}
	params := connCfg.RuntimeParams
	if params == nil {
		params = map[string]string{}
		connCfg.RuntimeParams = params
	}
	params["statement_timeout"] = strconv.FormatInt(d.Milliseconds(), 10)
}
