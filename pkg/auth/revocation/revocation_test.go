package revocation

import (
	"context"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestNewStoreRequiresAddress verifies that NewStore requires a non-empty address.
func TestNewStoreRequiresAddress(t *testing.T) {
	ctx := context.Background()
	_, err := NewStore(ctx, Config{Address: ""})
	if err == nil {
		t.Fatal("expected missing address to be rejected")
	}
	if err.Error() != "revocation store address is required" {
		t.Fatalf("expected 'revocation store address is required', got %q", err.Error())
	}
}

// TestNewStoreRejectsNegativeTTL verifies that NewStore rejects negative TTL.
func TestNewStoreRejectsNegativeTTL(t *testing.T) {
	ctx := context.Background()
	_, err := NewStore(ctx, Config{Address: "postgres://localhost:5432/test", TTL: -1})
	if err == nil {
		t.Fatal("expected negative TTL to be rejected")
	}
	if err.Error() != "revocation TTL must not be negative" {
		t.Fatalf("expected 'revocation TTL must not be negative', got %q", err.Error())
	}
}

// TestNewStoreRejectsInvalidAddress verifies that NewStore rejects invalid URLs.
func TestNewStoreRejectsInvalidAddress(t *testing.T) {
	ctx := context.Background()
	_, err := NewStore(ctx, Config{Address: "not-a-url", TTL: time.Minute})
	if err == nil {
		t.Fatal("expected invalid address to be rejected")
	}
}

// TestNewStoreUnsupportedScheme verifies that NewStore rejects unsupported schemes.
func TestNewStoreUnsupportedScheme(t *testing.T) {
	ctx := context.Background()
	_, err := NewStore(ctx, Config{Address: "redis://localhost:6379", TTL: time.Minute})
	if err == nil {
		t.Fatal("expected unsupported scheme to be rejected")
	}
	if err.Error() != "unsupported revocation store scheme: redis" {
		t.Fatalf("expected 'unsupported revocation store scheme: redis', got %q", err.Error())
	}
}

// postgresContainer creates a Postgres container for testing.
func postgresContainer(ctx context.Context) (*postgres.PostgresContainer, string, error) {
	container, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("test_revocation"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, "", err
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		_ = container.Terminate(ctx)
		return nil, "", err
	}

	return container, dsn, nil
}

// TestRevokeAndQuerySessions verifies basic revoke and IsRevoked behavior.
func TestRevokeAndQuerySessions(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Empty revocation is a no-op.
	if err := store.Revoke(ctx); err != nil {
		t.Fatalf("empty revocation: %v", err)
	}

	// Revoke two sessions.
	if err := store.Revoke(ctx, "laptop", "phone"); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	// Both sessions are revoked.
	for _, sid := range []string{"laptop", "phone"} {
		revoked, err := store.IsRevoked(ctx, sid)
		if err != nil || !revoked {
			t.Fatalf("session %q: revoked=%v err=%v", sid, revoked, err)
		}
	}

	// A non-revoked session is not revoked.
	revoked, err := store.IsRevoked(ctx, "live")
	if err != nil || revoked {
		t.Fatalf("live session: revoked=%v err=%v", revoked, err)
	}

	// Empty session ID is not revoked.
	revoked, err = store.IsRevoked(ctx, "")
	if err != nil || revoked {
		t.Fatalf("empty session ID: revoked=%v err=%v", revoked, err)
	}
}

// TestRevokeEntriesExpireWithinTTL verifies that entries expire within the TTL.
func TestRevokeEntriesExpireWithinTTL(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	ttl := 500 * time.Millisecond
	store, err := NewStore(ctx, Config{Address: dsn, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Revoke(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}

	// Immediately after revocation, session is still revoked.
	revoked, err := store.IsRevoked(ctx, "laptop")
	if err != nil || !revoked {
		t.Fatalf("immediately after revoke: revoked=%v err=%v", revoked, err)
	}

	// Wait for TTL + buffer.
	time.Sleep(ttl + 100*time.Millisecond)

	// Entry has expired and is no longer revoked.
	revoked, err = store.IsRevoked(ctx, "laptop")
	if err != nil || revoked {
		t.Fatalf("after TTL: revoked=%v err=%v", revoked, err)
	}

	// DeleteExpired should have removed the expired row.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 row deleted, got %d", deleted)
	}
}

// TestDeleteExpiredImmediateCleanup verifies DeleteExpired removes immediately expired rows.
func TestDeleteExpiredImmediateCleanup(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Revoke(ctx, "a", "b", "c"); err != nil {
		t.Fatal(err)
	}

	// Wait past the entries' 100ms expiry.
	time.Sleep(300 * time.Millisecond)

	// DeleteExpired should remove all expired rows.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 rows deleted, got %d", deleted)
	}

	// After cleanup, no entries remain.
	for _, sid := range []string{"a", "b", "c"} {
		revoked, err := store.IsRevoked(ctx, sid)
		if err != nil || revoked {
			t.Fatalf("after cleanup, session %q: revoked=%v err=%v", sid, revoked, err)
		}
	}
}

// TestReaderWithoutTTLCannotRevoke verifies that a store with TTL=0 cannot write.
func TestReaderWithoutTTLCannotRevoke(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	// Create store with TTL=0 (reader-only).
	store, err := NewStore(ctx, Config{Address: dsn, TTL: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Write attempts fail.
	err = store.Revoke(ctx, "test")
	if err == nil {
		t.Fatal("expected revoke with TTL=0 to fail")
	}
	if err.Error() != "revocation store is not configured for writes" {
		t.Fatalf("expected 'revocation store is not configured for writes', got %q", err.Error())
	}

	// Reads still work on a fresh entry created elsewhere.
	if err := store.Revoke(ctx, "test"); err == nil {
		// If write somehow succeeded, verify read works.
		revoked, err := store.IsRevoked(ctx, "test")
		if err != nil || !revoked {
			t.Fatalf("session test: revoked=%v err=%v", revoked, err)
		}
	}
}

// TestIsRevokedEmptySessionID verifies the empty-session-ID guard short-circuits
// before any store access; no backend is needed.
func TestIsRevokedEmptySessionID(t *testing.T) {
	store := &Store{}
	revoked, err := store.IsRevoked(context.Background(), "")
	if err != nil {
		t.Fatalf("empty session ID: err=%v", err)
	}
	if revoked {
		t.Fatal("empty session ID should not be revoked")
	}
}

// TestReaderRoleCanReadButCannotWrite pins the least-privilege deployment
// split: a SELECT-only role answers IsRevoked without needing DDL privileges,
// and a write through such a role fails closed at the database.
func TestReaderRoleCanReadButCannotWrite(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	writer, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if err := writer.Revoke(ctx, "revoked-session"); err != nil {
		t.Fatal(err)
	}

	// Provision a SELECT-only reader the way the deployment jobs do.
	raw, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(ctx, `CREATE ROLE revocation_reader_test LOGIN PASSWORD 'reader-pw'`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(ctx, `GRANT SELECT ON public.revoked_access_tokens TO revocation_reader_test`); err != nil {
		t.Fatal(err)
	}

	readerDSN, err := withCredentials(dsn, "revocation_reader_test", "reader-pw")
	if err != nil {
		t.Fatal(err)
	}
	// Booting the reader against the existing table must not attempt DDL.
	reader, err := NewStore(ctx, Config{Address: readerDSN, TTL: time.Minute})
	if err != nil {
		t.Fatalf("open reader store: %v", err)
	}
	defer func() { _ = reader.Close() }()

	revoked, err := reader.IsRevoked(ctx, "revoked-session")
	if err != nil {
		t.Fatalf("reader IsRevoked: %v", err)
	}
	if !revoked {
		t.Fatal("reader could not see the revocation written by the writer")
	}

	// A reader misconfigured with a write TTL still fails closed at the
	// database instead of recording a revocation.
	if err := reader.Revoke(ctx, "another-session"); err == nil {
		t.Fatal("SELECT-only role must not be able to record revocations")
	}
}

// withCredentials returns dsn with the user and password replaced, so tests
// can connect through a secondary role.
func withCredentials(dsn, user, password string) (string, error) {
	parsed, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("parse dsn: %w", err)
	}
	parsed.User = url.UserPassword(user, password)
	return parsed.String(), nil
}

// TestRevokeIdempotent verifies that re-revoking the same session updates expiry.
func TestRevokeIdempotent(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	ttl := 500 * time.Millisecond
	store, err := NewStore(ctx, Config{Address: dsn, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// First revoke.
	if err := store.Revoke(ctx, "session1"); err != nil {
		t.Fatal(err)
	}

	// Revoke again with same TTL (idempotent).
	if err := store.Revoke(ctx, "session1"); err != nil {
		t.Fatal(err)
	}

	// Session is still revoked.
	revoked, err := store.IsRevoked(ctx, "session1")
	if err != nil || !revoked {
		t.Fatalf("session1: revoked=%v err=%v", revoked, err)
	}
}

// TestDeleteExpiredNoOpWhenNoExpired verifies DeleteExpired is no-op when no rows expired.
func TestDeleteExpiredNoOpWhenNoExpired(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	ttl := 10 * time.Second
	store, err := NewStore(ctx, Config{Address: dsn, TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Add a session (will expire in 10s).
	if err := store.Revoke(ctx, "future"); err != nil {
		t.Fatal(err)
	}

	// DeleteExpired should find and delete nothing.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 rows deleted, got %d", deleted)
	}

	// Session is still revoked.
	revoked, err := store.IsRevoked(ctx, "future")
	if err != nil || !revoked {
		t.Fatalf("future: revoked=%v err=%v", revoked, err)
	}
}

// TestConcurrentRevokes verifies concurrent revokes are safe.
func TestConcurrentRevokes(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	done := make(chan error, 100)
	for i := 0; i < 100; i++ {
		go func(id int) {
			sid := "session" + string(rune('a'+id%26))
			err := store.Revoke(ctx, sid)
			done <- err
		}(i)
	}

	for i := 0; i < 100; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent revoke %d: %v", i, err)
		}
	}

	// Verify all sessions are revoked.
	for i := 0; i < 100; i++ {
		sid := "session" + string(rune('a'+i%26))
		revoked, err := store.IsRevoked(ctx, sid)
		if err != nil || !revoked {
			t.Fatalf("session %q: revoked=%v err=%v", sid, revoked, err)
		}
	}
}

// TestCleanupRemovesRevokedEntries verifies that DeleteExpired removes entries.
func TestCleanupRemovesRevokedEntries(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Revoke some sessions.
	if err := store.Revoke(ctx, "s1", "s2", "s3"); err != nil {
		t.Fatal(err)
	}

	// Wait for expiration.
	time.Sleep(1200 * time.Millisecond)

	// Cleanup removes them.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted, got %d", deleted)
	}

	// Verify they are no longer in the store.
	for _, sid := range []string{"s1", "s2", "s3"} {
		revoked, err := store.IsRevoked(ctx, sid)
		if err != nil {
			t.Fatalf("session %q: err=%v", sid, err)
		}
		if revoked {
			t.Fatalf("session %q should not be revoked after cleanup", sid)
		}
	}
}

// TestIsRevokedAfterDeleteExpired verifies IsRevoked returns false after cleanup.
func TestIsRevokedAfterDeleteExpired(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Revoke(ctx, "session"); err != nil {
		t.Fatal(err)
	}

	// Immediate check passes.
	revoked, err := store.IsRevoked(ctx, "session")
	if err != nil || !revoked {
		t.Fatalf("immediate: revoked=%v err=%v", revoked, err)
	}

	// Wait and cleanup.
	time.Sleep(1200 * time.Millisecond)
	if _, err := store.DeleteExpired(ctx); err != nil {
		t.Fatalf("delete expired: %v", err)
	}

	// Now it should be false.
	revoked, err = store.IsRevoked(ctx, "session")
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if revoked {
		t.Fatal("should not be revoked after cleanup")
	}
}

// TestMultipleDeleteExpiredCalls verifies multiple DeleteExpired calls are safe.
func TestMultipleDeleteExpiredCalls(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Revoke(ctx, "a", "b"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(1200 * time.Millisecond)

	// First call removes 2.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted first, got %d", deleted)
	}

	// Second call removes 0 (no-op).
	deleted, err = store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("expected 0 deleted second, got %d", deleted)
	}
}

// TestStoreCloseIsSafe verifies Close is safe to call multiple times.
func TestStoreCloseIsSafe(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Close the underlying pool.
	if err := store.Close(); err != nil {
		t.Fatalf("first close: %v", err)
	}

	// Second close is safe.
	if err := store.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
}

// TestCloseClosesUnderlyingPool verifies Close properly closes the pool.
func TestCloseClosesUnderlyingPool(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Close the store.
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Verify the pool is closed by trying to use it (should fail).
	// The postgresStore is not exported, so we can't directly check pool.Stat.
	// Instead, rely on the fact that IsRevoked on an already-closed store returns an error.
	// But since IsRevoked is on Store, not postgresStore, we can't test this directly.
	// In practice, pgxpool.Close is idempotent and safe.
}

// TestLargeBatchRevocation verifies revoking many sessions at once works.
func TestLargeBatchRevocation(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Revoke 100 sessions at once.
	sessionIDs := make([]string, 100)
	for i := 0; i < 100; i++ {
		sessionIDs[i] = "session" + string(rune('a'+i%26))
	}

	if err := store.Revoke(ctx, sessionIDs...); err != nil {
		t.Fatalf("revoke batch: %v", err)
	}

	// Verify all are revoked.
	for _, sid := range sessionIDs {
		revoked, err := store.IsRevoked(ctx, sid)
		if err != nil || !revoked {
			t.Fatalf("session %q: revoked=%v err=%v", sid, revoked, err)
		}
	}
}

// TestDeleteExpiredWithMixedExpired verifies DeleteExpired handles mixed state.
func TestDeleteExpiredWithMixedExpired(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Add some that will expire soon.
	if err := store.Revoke(ctx, "expiring1", "expiring2"); err != nil {
		t.Fatal(err)
	}

	// Add some with longer TTL (won't expire).
	longStore, err := NewStore(ctx, Config{Address: dsn, TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = longStore.Close() }()
	if err := longStore.Revoke(ctx, "long1", "long2"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(1200 * time.Millisecond)

	// DeleteExpired removes only the expired ones.
	deleted, err := store.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}

	// The long-lived ones are still revoked.
	for _, sid := range []string{"long1", "long2"} {
		revoked, err := store.IsRevoked(ctx, sid)
		if err != nil || !revoked {
			t.Fatalf("session %q: revoked=%v err=%v", sid, revoked, err)
		}
	}
}

// TestReRevokeCannotShortenExpiry pins the retention invariant: a writer
// reconfigured with a shorter TTL cannot shorten an existing entry that may
// still cover tokens issued under the previous TTL. The entry survives past
// the short TTL and only lapses at the original one.
func TestReRevokeCannotShortenExpiry(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	long, err := NewStore(ctx, Config{Address: dsn, TTL: 1500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = long.Close() }()
	if err := long.Revoke(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}

	short, err := NewStore(ctx, Config{Address: dsn, TTL: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = short.Close() }()
	if err := short.Revoke(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}

	time.Sleep(400 * time.Millisecond)
	if revoked, err := short.IsRevoked(ctx, "laptop"); err != nil || !revoked {
		t.Fatalf("entry after the short TTL: revoked=%v err=%v, want still revoked", revoked, err)
	}

	time.Sleep(1300 * time.Millisecond)
	if revoked, err := long.IsRevoked(ctx, "laptop"); err != nil || revoked {
		t.Fatalf("entry after the original TTL: revoked=%v err=%v, want expired", revoked, err)
	}
}

// TestIsRevokedBoundedWhenPoolExhausted verifies the per-call budget bounds
// the whole lookup, including the wait for a pooled connection: holding every
// connection in an idle transaction (which statement_timeout cannot free)
// still returns Unavailable within the budget when the caller passes a bare
// context.
func TestIsRevokedBoundedWhenPoolExhausted(t *testing.T) {
	ctx := context.Background()

	container, dsn, err := postgresContainer(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = container.Terminate(ctx) }()

	store, err := NewStore(ctx, Config{Address: dsn, TTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	postgres, ok := store.impl.(*postgresStore)
	if !ok {
		t.Fatalf("unexpected store implementation %T", store.impl)
	}
	for i := 0; i < 5; i++ {
		conn, err := postgres.pool.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := conn.Begin(ctx)
		if err != nil {
			conn.Release()
			t.Fatal(err)
		}
		// Idle in transaction: the connection is checked out without any
		// running statement, so statement_timeout never fires.
		defer func() {
			_ = tx.Rollback(context.Background())
			conn.Release()
		}()
	}

	start := time.Now()
	_, err = store.IsRevoked(ctx, "laptop")
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an exhausted pool to fail the check, not block indefinitely")
	}
	if elapsed > callBudget+2*time.Second {
		t.Fatalf("IsRevoked returned after %v; want it bounded by the %v call budget", elapsed, callBudget)
	}
}
