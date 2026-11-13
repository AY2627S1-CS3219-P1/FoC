// Package revocation tracks revoked access-token sessions so every service can
// reject outstanding access tokens promptly after their session is revoked,
// instead of waiting for the tokens to expire.
//
// The shared revocation store can be backed by any storage backend. The
// constructor returns a concrete implementation based on the address scheme;
// today's implementation is a direct PostgreSQL connection to a dedicated
// revocation table.
package revocation

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Config identifies the revocation store and, for writers, the lifetime an
// entry must outlive. Address is an opaque locator: for the current
// implementation, a postgres:// or postgresql:// URL pointing to a dedicated
// database.
type Config struct {
	Address string
	TTL     time.Duration
}

// Store is the shared revocation store used by the User Service (writer) and
// every token-verifying service (reader).
type Store struct {
	impl RevocationStore
	ttl  time.Duration
}

// RevocationStore is the internal contract implemented by the backends.
// It is exported only so test doubles in other packages can implement it;
// production consumers receive the concrete *Store.
type RevocationStore interface {
	Revoke(ctx context.Context, sessionIDs ...string) error
	IsRevoked(ctx context.Context, sessionID string) (bool, error)
	DeleteExpired(ctx context.Context) (int64, error)
	Close() error
}

// NewStore connects to the revocation store and verifies the connection eagerly
// so a missing or unreachable store fails at startup, not on the first request.
// Address must be a scheme:// URL; the scheme selects the backend (e.g.
// postgres:// or postgresql:// for the current PostgreSQL implementation).
func NewStore(ctx context.Context, config Config) (*Store, error) {
	if config.Address == "" {
		return nil, errors.New("revocation store address is required")
	}
	if config.TTL < 0 {
		return nil, errors.New("revocation TTL must not be negative")
	}

	u, err := url.Parse(config.Address)
	if err != nil {
		return nil, fmt.Errorf("invalid revocation store address: %w", err)
	}

	var impl RevocationStore
	switch u.Scheme {
	case "postgres", "postgresql":
		impl, err = newPostgresStore(ctx, config.Address, config.TTL)
	default:
		return nil, fmt.Errorf("unsupported revocation store scheme: %s", u.Scheme)
	}
	if err != nil {
		return nil, err
	}

	return &Store{impl: impl, ttl: config.TTL}, nil
}

// Revoke records sessions whose access tokens must stop being accepted. The
// caller invokes it after the database session revocation committed; entries
// expire once every access token that could carry these session IDs has
// expired, so expired sessions are harmless to include. Re-revoking a session
// extends its entry to the current expiry; it never shortens an existing one.
func (s *Store) Revoke(ctx context.Context, sessionIDs ...string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	if s.ttl <= 0 {
		return errors.New("revocation store is not configured for writes")
	}
	return s.impl.Revoke(ctx, sessionIDs...)
}

// IsRevoked reports whether any access token of the session was revoked. An
// error means the store is unreachable; callers must treat that as unavailable,
// never as "not revoked".
func (s *Store) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	return s.impl.IsRevoked(ctx, sessionID)
}

// DeleteExpired removes expired entries from the store. It is called by the
// User Service to bound the size of the underlying table/index. Readers do not
// need to call this; the IsRevoked predicate filters expired entries.
func (s *Store) DeleteExpired(ctx context.Context) (int64, error) {
	return s.impl.DeleteExpired(ctx)
}

// Close releases the store's resources.
func (s *Store) Close() error {
	return s.impl.Close()
}
