package infra

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
)

// VerifyDurability records the effective durability settings of the benchmark's
// Redis and Postgres instances and fails unless they are compatible with a
// durable-write run. Durable writes are only meaningful when the acknowledged
// write is really flushed to stable storage, so this is a hard gate: the
// benchmark fatals rather than silently measuring with appendfsync=everysec or
// synchronous_commit=off.
//
// It reports LOCAL durability only — that a single instance flushed the write —
// not replicated failover. The returned map is the effective configuration,
// written into the results meta so the run is reproducible.
func VerifyDurability(ctx context.Context, redisAddr, dsn string) (map[string]string, error) {
	settings := map[string]string{}

	// Redis: appendonly=yes (persist via AOF) and appendfsync=always (fsync each
	// acknowledged append). Redis CONFIG GET returns one value per key.
	rc := redis.NewClient(&redis.Options{Addr: redisAddr, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
	for _, key := range []string{"appendonly", "appendfsync"} {
		val, err := rc.ConfigGet(ctx, key).Result()
		if err != nil {
			_ = rc.Close()
			return nil, fmt.Errorf("redis CONFIG GET %s: %w", key, err)
		}
		settings["redis_"+key] = val[key]
	}
	_ = rc.Close()

	// Postgres: fsync=on and synchronous_commit=on mean every committed write is
	// flushed to disk before the client is acked.
	pc, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres connection: %w", err)
	}
	defer func() { _ = pc.Close(ctx) }()
	for _, name := range []string{"fsync", "synchronous_commit"} {
		var val string
		if err := pc.QueryRow(ctx, "SHOW "+name).Scan(&val); err != nil {
			return nil, fmt.Errorf("SHOW %s: %w", name, err)
		}
		settings["pg_"+name] = val
	}

	// The table must be a normal logged table (relpersistence='p'). A 't'
	// (temp) or 'u' (unlogged) table would silently drop writes on crash.
	var relpersistence string
	if err := pc.QueryRow(ctx, `SELECT relpersistence::text FROM pg_class WHERE relname = 'revoked_access_tokens'`).Scan(&relpersistence); err != nil {
		return nil, fmt.Errorf("inspect revoked_access_tokens persistence: %w", err)
	}
	settings["pg_relpersistence"] = relpersistence

	return settings, nil
}

// CheckDurability enforces the expected durable settings and returns a
// human-readable reason when any setting is off, so main can fail with an
// actionable message. An empty result means every expected setting matched.
func CheckDurability(settings map[string]string) string {
	var problems []string
	check := func(label, got, want string) {
		if got != want {
			problems = append(problems, fmt.Sprintf("%s=%s (want %s)", label, got, want))
		}
	}
	check("redis_appendonly", settings["redis_appendonly"], "yes")
	check("redis_appendfsync", settings["redis_appendfsync"], "always")
	check("pg_fsync", settings["pg_fsync"], "on")
	check("pg_synchronous_commit", settings["pg_synchronous_commit"], "on")
	check("pg_relpersistence", settings["pg_relpersistence"], "p")
	return strings.Join(problems, "; ")
}

// ParseWritePercentages parses a comma list of write percentages (0..100) into
// fractions (0..1). "10" => 0.10. A value outside 0..100 or non-numeric fails.
func ParseWritePercentages(s string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.ParseFloat(part, 64)
		if err != nil || n < 0 || n > 100 {
			return nil, fmt.Errorf("invalid write percentage %q (want 0..100)", part)
		}
		out = append(out, n/100)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no write percentages given")
	}
	return out, nil
}
