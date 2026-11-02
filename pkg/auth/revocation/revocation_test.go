package revocation

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func newTestStore(t *testing.T, ttl time.Duration) (*Store, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	store, err := NewStore(context.Background(), Config{Addr: server.Addr(), TTL: ttl})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, server
}

func TestNewStoreRequiresAddress(t *testing.T) {
	if _, err := NewStore(context.Background(), Config{}); err == nil {
		t.Fatal("expected missing Redis address to be rejected")
	}
}

func TestRevokeAndQuerySessions(t *testing.T) {
	ctx := context.Background()
	store, server := newTestStore(t, time.Minute)

	if err := store.Revoke(ctx); err != nil {
		t.Fatalf("empty revocation: %v", err)
	}
	if err := store.Revoke(ctx, "laptop", "phone"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	for _, sessionID := range []string{"laptop", "phone"} {
		revoked, err := store.IsRevoked(ctx, sessionID)
		if err != nil || !revoked {
			t.Fatalf("session %q: revoked=%v err=%v", sessionID, revoked, err)
		}
		if !server.Exists(Key(sessionID)) {
			t.Fatalf("session %q entry missing", sessionID)
		}
	}
	if revoked, err := store.IsRevoked(ctx, "live"); err != nil || revoked {
		t.Fatalf("live session: revoked=%v err=%v", revoked, err)
	}
	if revoked, err := store.IsRevoked(ctx, ""); err != nil || revoked {
		t.Fatalf("empty session ID: revoked=%v err=%v", revoked, err)
	}
}

func TestRevokeEntriesExpireWithinAccessTokenLifetime(t *testing.T) {
	ctx := context.Background()
	ttl := time.Minute
	store, server := newTestStore(t, ttl)

	if err := store.Revoke(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}
	remaining := server.TTL(Key("laptop"))
	if remaining <= 0 || remaining > ttl {
		t.Fatalf("entry TTL=%v, want within (0, %v]", remaining, ttl)
	}

	// Once the maximum access-token lifetime passes, every token the entry
	// could cover has expired, so the entry must be gone.
	server.FastForward(ttl)
	if server.Exists(Key("laptop")) {
		t.Fatal("entry outlived the access-token lifetime")
	}
	revoked, err := store.IsRevoked(ctx, "laptop")
	if err != nil || revoked {
		t.Fatalf("expired entry still revokes: revoked=%v err=%v", revoked, err)
	}
}

func TestReaderWithoutTTLCanQueryButNotRevoke(t *testing.T) {
	ctx := context.Background()
	writer, server := newTestStore(t, time.Minute)
	reader, err := NewStore(ctx, Config{Addr: server.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })

	if err := writer.Revoke(ctx, "laptop"); err != nil {
		t.Fatal(err)
	}
	if revoked, err := reader.IsRevoked(ctx, "laptop"); err != nil || !revoked {
		t.Fatalf("reader: revoked=%v err=%v", revoked, err)
	}
	if err := reader.Revoke(ctx, "laptop"); err == nil {
		t.Fatal("reader without TTL must not write")
	}
}

func TestRevokeFailsWhenRedisIsDown(t *testing.T) {
	ctx := context.Background()
	store, server := newTestStore(t, time.Minute)
	server.Close()
	if err := store.Revoke(ctx, "laptop"); err == nil {
		t.Fatal("expected write against a closed Redis to fail")
	}
	if _, err := store.IsRevoked(ctx, "laptop"); err == nil {
		t.Fatal("expected query against a closed Redis to fail")
	}
}

func TestNewStoreFailsWhenUnreachable(t *testing.T) {
	// Port 1 is reserved and refuses connections; the eager ping must surface it.
	if _, err := NewStore(context.Background(), Config{Addr: "127.0.0.1:1"}); err == nil {
		t.Fatal("expected unreachable Redis to be rejected")
	}
}
