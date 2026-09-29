package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

// setupAuthStore runs only against an explicitly configured throwaway database
// because it truncates user data before each test.
func setupAuthStore(t *testing.T) (*Store, *gorm.DB) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("TRUNCATE users CASCADE").Error; err != nil {
		t.Fatal(err)
	}
	return New(db), db
}

func testDigest(value string) [32]byte { return sha256.Sum256([]byte(value)) }

func createStoreUser(t *testing.T, store *Store, email string) models.User {
	t.Helper()
	user := models.User{Email: email, DisplayName: "User", Role: models.RoleUser}
	if err := store.Users.Create(context.Background(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

func TestUsersAuthPersistence(t *testing.T) {
	ctx := context.Background()
	store, _ := setupAuthStore(t)

	user := createStoreUser(t, store, "user@example.com")
	if user.ID == uuid.Nil {
		t.Fatal("database did not assign a user ID")
	}
	if got, err := store.GetByEmail(ctx, "USER@example.com"); err != nil || got.ID != user.ID {
		t.Fatalf("user by email: %+v, %v", got, err)
	}
	if got, err := store.GetByID(ctx, user.ID); err != nil || got.Email != user.Email {
		t.Fatalf("user by ID: %+v, %v", got, err)
	}
	duplicate := models.User{Email: user.Email, DisplayName: "Duplicate", Role: models.RoleUser}
	if err := store.Users.Create(ctx, &duplicate); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate user: %v", err)
	}
	if _, err := store.GetByID(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing user: %v", err)
	}
}

func TestDomainWhitelist(t *testing.T) {
	ctx := context.Background()
	store, db := setupAuthStore(t)

	if allowed, err := store.Admin.Allows(ctx, "example.com"); err != nil || allowed {
		t.Fatalf("empty whitelist should deny registration: allowed=%v err=%v", allowed, err)
	}
	if err := db.Create(&models.AllowedEmailDomain{Domain: "u.nus.edu"}).Error; err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"u.nus.edu", "U.NUS.EDU"} {
		if allowed, err := store.Admin.Allows(ctx, domain); err != nil || !allowed {
			t.Errorf("allowed domain %q: allowed=%v err=%v", domain, allowed, err)
		}
	}
	if allowed, err := store.Admin.Allows(ctx, "example.com"); err != nil || allowed {
		t.Fatalf("unlisted domain: allowed=%v err=%v", allowed, err)
	}
}

func TestAuthTokensPersistence(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, _ := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	loginHash := testDigest("login")
	login := models.AuthToken{TokenHash: loginHash[:], Purpose: models.TokenPurposeLogin,
		Email: user.Email, UserID: &user.ID, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.AuthTokens.Create(ctx, &login); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthTokens.Consume(ctx, loginHash, models.TokenPurposeRegister, now); !errors.Is(err, ErrChallengeRejected) {
		t.Fatalf("wrong token purpose: %v", err)
	}
	consumed, err := store.AuthTokens.Consume(ctx, loginHash, models.TokenPurposeLogin, now)
	if err != nil || consumed.ID != login.ID {
		t.Fatalf("consume token: %+v, %v", consumed, err)
	}
	if _, err := store.AuthTokens.Consume(ctx, loginHash, models.TokenPurposeLogin, now); !errors.Is(err, ErrChallengeRejected) {
		t.Fatalf("reused token: %v", err)
	}

	concurrentHash := testDigest("concurrent-login")
	concurrent := models.AuthToken{TokenHash: concurrentHash[:], Purpose: models.TokenPurposeLogin,
		Email: user.Email, UserID: &user.ID, CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.AuthTokens.Create(ctx, &concurrent); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := store.AuthTokens.Consume(ctx, concurrentHash, models.TokenPurposeLogin, now)
			results <- err
		}()
	}
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, ErrChallengeRejected)) ||
		(second == nil && errors.Is(first, ErrChallengeRejected))) {
		t.Fatalf("concurrent token consumption: %v, %v", first, second)
	}
}

func TestStoreTransactionRollback(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, db := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	rollbackHash := testDigest("rollback")
	rollbackToken := models.AuthToken{TokenHash: rollbackHash[:], Purpose: models.TokenPurposeRegister,
		Email: "new@example.com", CreatedAt: now, ExpiresAt: now.Add(time.Minute)}
	if err := store.AuthTokens.Create(ctx, &rollbackToken); err != nil {
		t.Fatal(err)
	}
	rollbackSessionHash := testDigest("rollback-session")
	rollbackSession := models.Session{ID: uuid.New(), UserID: user.ID, TokenHash: rollbackSessionHash[:],
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	rollbackErr := errors.New("roll back")
	err := store.WithTransaction(ctx, func(tx *Store) error {
		if _, err := tx.AuthTokens.Consume(ctx, rollbackHash, models.TokenPurposeRegister, now); err != nil {
			return err
		}
		if err := tx.Sessions.Create(ctx, &rollbackSession); err != nil {
			return err
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("transaction error: %v", err)
	}
	if _, err := store.AuthTokens.Consume(ctx, rollbackHash, models.TokenPurposeRegister, now); err != nil {
		t.Fatalf("rollback left token consumed: %v", err)
	}
	var count int64
	if err := db.Model(&models.Session{}).Where("id = ?", rollbackSession.ID).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("rollback left session: count=%d err=%v", count, err)
	}
}

func TestSessionsPersistence(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	store, _ := setupAuthStore(t)
	user := createStoreUser(t, store, "user@example.com")

	sessionHash := testDigest("session")
	session := models.Session{ID: uuid.New(), UserID: user.ID, TokenHash: sessionHash[:],
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := store.Sessions.Create(ctx, &session); err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions.Revoke(ctx, session.ID, testDigest("stale-session"), now); !errors.Is(err, ErrSessionRejected) {
		t.Fatalf("stale session hash: %v", err)
	}
	if err := store.Sessions.Revoke(ctx, session.ID, sessionHash, now); err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions.Revoke(ctx, session.ID, sessionHash, now); !errors.Is(err, ErrSessionRejected) {
		t.Fatalf("repeated revocation: %v", err)
	}
}
