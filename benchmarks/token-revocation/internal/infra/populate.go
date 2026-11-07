package infra

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// keyPrefix namespaces the seeded entries. It must stay identical to
// checker.KeyPrefix so a probe against one store hits exactly what the other
// holds: the benchmark compares the two stores on an identical dataset.
const keyPrefix = "revoked_access_tokens:"

// Populate seeds the given session keys into Redis and Postgres so a later check
// is a present-key lookup in both. Redis gets SET <key> <unixts> EX ttl per key;
// Postgres gets one row (sid) per key, letting revoked_at default to now().
// FLUSHALL/TRUNCATE make reruns idempotent. ttl bounds how long an entry would
// outlive a token.
//
// Postgres seeding uses chunked multi-value INSERT (one round trip per chunk),
// which depends only on pgx and redis.
func Populate(ctx context.Context, redisAddr, dsn string, sids []string, ttl time.Duration) error {
	// Redis: flush then pipeline the keys in chunks.
	client := redis.NewClient(&redis.Options{
		Addr:         redisAddr,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	if err := client.FlushAll(ctx).Err(); err != nil {
		client.Close()
		return fmt.Errorf("flush redis: %w", err)
	}
	const chunk = 10_000
	for i := 0; i < len(sids); i += chunk {
		end := i + chunk
		if end > len(sids) {
			end = len(sids)
		}
		pipe := client.Pipeline()
		for _, sid := range sids[i:end] {
			pipe.Set(ctx, keyPrefix+sid, time.Now().Unix(), ttl)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			client.Close()
			return fmt.Errorf("redis populate chunk %d: %w", i, err)
		}
	}
	client.Close()

	// Postgres: truncate, then chunked multi-value INSERT of every key with an
	// expires_at well past the sweep (ttl), so seeded entries never lapse mid-run.
	raw, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return fmt.Errorf("open postgres connection: %w", err)
	}
	defer func() { _ = raw.Close(ctx) }()
	if _, err := raw.Exec(ctx, "TRUNCATE revoked_access_tokens"); err != nil {
		return fmt.Errorf("truncate: %w", err)
	}
	const batch = 5_000
	exp := time.Now().Add(ttl) // single expiry for the whole batch; still far out
	for i := 0; i < len(sids); i += batch {
		end := i + batch
		if end > len(sids) {
			end = len(sids)
		}
		var sb strings.Builder
		sb.WriteString("INSERT INTO revoked_access_tokens (sid, expires_at) VALUES ")
		args := make([]any, 0, 2*(end-i))
		for j := i; j < end; j++ {
			if j > i {
				sb.WriteByte(',')
			}
			fmt.Fprintf(&sb, "($%d, $%d)", 2*(j-i)+1, 2*(j-i)+2)
			args = append(args, sids[j], exp)
		}
		if _, err := raw.Exec(ctx, sb.String(), args...); err != nil {
			return fmt.Errorf("multi insert: %w", err)
		}
	}
	return nil
}
