package main

import (
	"context"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/revocation"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestRevocationPostgresStoreIntegration proves the PostgreSQL-backed revocation
// store works end-to-end: a revoked session is correctly identified, entries
// expire within the TTL, and DeleteExpired removes expired rows.
func TestRevocationPostgresStoreIntegration(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	// Start a PostgreSQL container for the revocation store.
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("test_revocation"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	// Create the revocation store.
	ttl := 500 * time.Millisecond
	store, err := revocation.NewStore(ctx, revocation.Config{Address: dsn, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	// Revoked session is detected.
	if err := store.Revoke(ctx, "laptop"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked, err := store.IsRevoked(ctx, "laptop")
	if err != nil || !revoked {
		t.Fatalf("laptop: revoked=%v err=%v", revoked, err)
	}

	// Non-revoked session is not detected.
	revoked, err = store.IsRevoked(ctx, "live")
	if err != nil || revoked {
		t.Fatalf("live: revoked=%v err=%v", revoked, err)
	}

	// Wait for TTL + buffer.
	time.Sleep(ttl + 100*time.Millisecond)

	// Entry has expired and is no longer revoked.
	revoked, err = store.IsRevoked(ctx, "laptop")
	if err != nil || revoked {
		t.Fatalf("after TTL: revoked=%v err=%v", revoked, err)
	}

	// DeleteExpired removes expired rows.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 row deleted, got %d", deleted)
	}

	// Verify via direct DB query.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	defer pool.Close()

	var count int
	err = pool.QueryRow(ctx, "SELECT COUNT(*) FROM revoked_access_tokens").Scan(&count)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 rows after cleanup, got %d", count)
	}
}
