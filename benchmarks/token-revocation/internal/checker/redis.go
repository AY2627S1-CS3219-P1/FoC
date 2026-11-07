package checker

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyPrefix is the Redis key prefix for revoked access-token sessions. It
// matches the key the production authentication middleware reads.
const KeyPrefix = "revoked_access_tokens:"

// Redis checks revocation against the Redis dictionary the production
// middleware reads.
type Redis struct {
	client *redis.Client
}

// NewRedis dials Redis and verifies the connection before returning.
func NewRedis(ctx context.Context, addr string) (*Redis, error) {
	client := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  3 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})

	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()

		return nil, fmt.Errorf("ping redis at %s: %w", addr, err)
	}

	return &Redis{client: client}, nil
}

// Revoke records a durable revocation for sessionID: SET <key> <unix-ts> EX
// ttl. With appendfsync always the acknowledged append is fsynced, so the
// return value means the entry survived a crash. sessionID must be a unique ID
// the benchmark owns so this is an insert, not an overwrite of an existing
// entry.
func (r *Redis) Revoke(ctx context.Context, sessionID string, ttl time.Duration) error {
	if err := r.client.Set(ctx, KeyPrefix+sessionID, time.Now().Unix(), ttl).Err(); err != nil {
		return fmt.Errorf("revoke redis session %q: %w", sessionID, err)
	}

	return nil
}

// Delete removes the given keys in one pipeline, resetting the write-key
// population between measured windows.
func (r *Redis) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	pipe := r.client.Pipeline()
	for _, id := range ids {
		pipe.Del(ctx, KeyPrefix+id)
	}

	if _, err := pipe.Exec(ctx); err != nil {
		return fmt.Errorf("delete redis sessions: %w", err)
	}

	return nil
}

// IsRevoked runs one EXISTS lookup on the session's revocation key.
func (r *Redis) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	if sessionID == "" {
		return false, nil
	}

	count, err := r.client.Exists(ctx, KeyPrefix+sessionID).Result()
	if err != nil {
		return false, fmt.Errorf("check redis revocation for session %q: %w", sessionID, err)
	}

	return count > 0, nil
}

// Close closes the Redis client.
func (r *Redis) Close() error {
	return r.client.Close()
}
