// Package checker provides the revocation lookups the token-revocation benchmark compares.
package checker

import (
	"context"
	"time"
)

// Checker reports whether one session's access tokens have been revoked. It
// backs the three storage strategies the benchmark compares; every
// implementation fails closed: an error means the store was unreachable and the
// caller must treat the request as rejected, never as accepted.
type Checker interface {
	// IsRevoked performs exactly one revocation lookup for one session ID.
	IsRevoked(ctx context.Context, sessionID string) (bool, error)
	// Revoke durably records a fresh revocation for sessionID, expiring after
	// ttl, and returns only after the write is acknowledged (Redis AOF fsync, or
	// a committed Postgres transaction). It must not overwrite an existing
	// entry the benchmark does not own; callers supply unique IDs.
	Revoke(ctx context.Context, sessionID string, ttl time.Duration) error
	// Close releases the checker's resources.
	Close() error
}

// Resetter is implemented by checkers that can delete a batch of keys, used to
// reset the write-key population between measured windows so earlier writes do
// not contaminate later repetitions.
type Resetter interface {
	// Delete removes the given session IDs in bulk.
	Delete(ctx context.Context, ids []string) error
}
