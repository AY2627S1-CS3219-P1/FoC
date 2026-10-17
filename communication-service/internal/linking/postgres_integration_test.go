package linking

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	"github.com/google/uuid"
)

// newService needs TEST_DATABASE_URL pointing at a throwaway database, because
// it wipes the linking tables.
func newService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE telegram_links, telegram_link_codes`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	return &Service{Store: &PostgresStore{Pool: pool}, Now: func() time.Time { return now }}, &now
}

func TestLinkingLifecycle(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	alice := uuid.New()

	if _, err := svc.Get(ctx, alice); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("before linking: %v", err)
	}
	code, expiresAt, err := svc.CreateCode(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 32 || !expiresAt.Equal(svc.Now().Add(CodeLifetime)) {
		t.Fatalf("code %q expires %v", code, expiresAt)
	}

	link, err := svc.Redeem(ctx, code, Chat{ID: 100, Username: "alice_tg"})
	if err != nil || link.UserID != alice || link.ChatID != 100 {
		t.Fatalf("Redeem = %+v, %v", link, err)
	}
	got, err := svc.Get(ctx, alice)
	if err != nil || got.ChatID != 100 || got.Username != "alice_tg" {
		t.Fatalf("Get = %+v, %v", got, err)
	}

	if _, err := svc.Redeem(ctx, code, Chat{ID: 999}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("reusing a code: %v", err)
	}

	if err := svc.Unlink(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if err := svc.Unlink(ctx, alice); err != nil {
		t.Fatalf("unlinking twice: %v", err)
	}
	if _, err := svc.Get(ctx, alice); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("after unlinking: %v", err)
	}
}

func TestCodesExpireAndAreReplaced(t *testing.T) {
	svc, now := newService(t)
	ctx := context.Background()
	alice := uuid.New()

	first, _, _ := svc.CreateCode(ctx, alice)
	second, _, _ := svc.CreateCode(ctx, alice)
	if _, err := svc.Redeem(ctx, first, Chat{ID: 100}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("replaced code: %v", err)
	}

	*now = now.Add(CodeLifetime)
	if _, err := svc.Redeem(ctx, second, Chat{ID: 100}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("expired code: %v", err)
	}
	if _, err := svc.Redeem(ctx, "never-issued", Chat{ID: 100}); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("unknown code: %v", err)
	}
}

func TestOneChatPerAccountAndAccountPerChat(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	alice, bob := uuid.New(), uuid.New()
	redeem := func(user uuid.UUID, chat int64) {
		t.Helper()
		code, _, err := svc.CreateCode(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Redeem(ctx, code, Chat{ID: chat}); err != nil {
			t.Fatal(err)
		}
	}

	redeem(alice, 100)
	redeem(alice, 200) // Alice switches Telegram accounts.
	if got, _ := svc.Get(ctx, alice); got.ChatID != 200 {
		t.Fatalf("alice chat = %d, want 200", got.ChatID)
	}

	redeem(bob, 200) // The same Telegram chat links Bob's FoC account.
	if got, _ := svc.Get(ctx, bob); got.ChatID != 200 {
		t.Fatalf("bob chat = %d, want 200", got.ChatID)
	}
	if _, err := svc.Get(ctx, alice); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("alice should lose chat 200: %v", err)
	}

	if err := svc.UnlinkChat(ctx, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Get(ctx, bob); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("after block: %v", err)
	}
}

func TestConcurrentCodesLeaveOneUnused(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	alice := uuid.New()

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := svc.CreateCode(ctx, alice); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("CreateCode: %v", err)
	}
	var unused int
	pool := svc.Store.(*PostgresStore).Pool
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM telegram_link_codes WHERE user_id = $1 AND used_at IS NULL`, alice).Scan(&unused); err != nil {
		t.Fatal(err)
	}
	if unused != 1 {
		t.Fatalf("unused codes = %d, want 1", unused)
	}
}

func TestConcurrentRedemptionsFromOneChat(t *testing.T) {
	svc, _ := newService(t)
	ctx := context.Background()
	users := make([]uuid.UUID, 8)
	codes := make([]string, len(users))
	for i := range users {
		users[i] = uuid.New()
		code, _, err := svc.CreateCode(ctx, users[i])
		if err != nil {
			t.Fatal(err)
		}
		codes[i] = code
	}

	var wg sync.WaitGroup
	errs := make(chan error, len(codes))
	for _, code := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := svc.Redeem(ctx, code, Chat{ID: 300}); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Redeem: %v", err)
	}
	linked := 0
	for _, user := range users {
		if link, err := svc.Get(ctx, user); err == nil && link.ChatID == 300 {
			linked++
		}
	}
	if linked != 1 {
		t.Fatalf("accounts linked to chat 300 = %d, want 1", linked)
	}
}
