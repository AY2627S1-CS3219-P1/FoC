// Package revocation tracks revoked access-token sessions in Redis so every
// service can reject outstanding access tokens promptly after their session is
// revoked, instead of waiting for the tokens to expire.
//
// Each revoked session is one dictionary entry, `revoked_access_tokens:<sid>`,
// with a per-entry TTL equal to the maximum access-token lifetime. Because a
// revoked session can never be revived and refresh always mints a new session
// ID, every access token an entry could cover is guaranteed expired by the time
// the entry lapses.
package revocation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyPrefix namespaces the per-session revocation entries. All services must
// agree on it; it is the "revoked_access_tokens" dictionary.
const KeyPrefix = "revoked_access_tokens:"

// Config constructs a Store. Readers only need Addr; writers must set TTL to
// the maximum access-token lifetime so entries outlive every token they cover.
type Config struct {
	Addr string
	TTL  time.Duration
}

// Store is the Redis-backed revocation dictionary shared by the User Service
// (writer) and every token-verifying service (reader).
type Store struct {
	client *redis.Client
	ttl    time.Duration
}

// NewStore connects to Redis and verifies the connection eagerly so a missing
// or unreachable revocation store fails at startup, not on the first request.
func NewStore(ctx context.Context, config Config) (*Store, error) {
	if config.Addr == "" {
		return nil, errors.New("redis address is required")
	}
	if config.TTL < 0 {
		return nil, errors.New("revocation TTL must not be negative")
	}
	client := redis.NewClient(&redis.Options{
		Addr: config.Addr,
		// Fail fast so fail-closed verification rejects quickly instead of
		// hanging the request until the handler timeout.
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return &Store{client: client, ttl: config.TTL}, nil
}

// Key is the dictionary entry for one session's access tokens.
func Key(sessionID string) string { return KeyPrefix + sessionID }

// Revoke records sessions whose access tokens must stop being accepted. The
// caller invokes it after the database session revocation committed; entries
// expire once every access token that could carry these session IDs has
// expired, so expired sessions are harmless to include.
func (s *Store) Revoke(ctx context.Context, sessionIDs ...string) error {
	if len(sessionIDs) == 0 {
		return nil
	}
	if s.ttl <= 0 {
		return errors.New("revocation store is not configured for writes")
	}
	revokedAt := strconv.FormatInt(time.Now().UTC().Unix(), 10)
	pipe := s.client.Pipeline()
	for _, sessionID := range sessionIDs {
		pipe.Set(ctx, Key(sessionID), revokedAt, s.ttl)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("record revoked access tokens: %w", err)
	}
	return nil
}

// IsRevoked reports whether any access token of the session was revoked. An
// error means Redis is unreachable; callers must treat that as unavailable,
// never as "not revoked".
func (s *Store) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	count, err := s.client.Exists(ctx, Key(sessionID)).Result()
	if err != nil {
		return false, fmt.Errorf("check revoked access tokens: %w", err)
	}
	return count > 0, nil
}

// Close releases the Redis connection.
func (s *Store) Close() error { return s.client.Close() }
