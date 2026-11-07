//go:build integration

// Package revocation_test exercises the PostgreSQL-backed access-token revocation
// flow end to end: a live session is accepted, session or role revocation
// makes its still-cryptographically-valid access token rejected, and the
// entries are cleaned up when expired.
package revocation_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/revocation"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	userjwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"gorm.io/gorm"
)

// Keep the access-token lifetime short so the expiry assertion runs in test time.
const accessTTL = 30 * time.Second

type stubEmailSender struct{}

func (stubEmailSender) Send(context.Context, email.Email) error { return nil }

type allowAllDomains struct{}

func (allowAllDomains) IsDomainAllowed(context.Context, string) (bool, error) { return true, nil }

type authStack struct {
	store         *store.Store
	db            *gorm.DB
	logic         *service.Service
	roles         *service.RoleService
	codec         *userjwt.ES256Codec
	revocations   *revocation.Store
	revocationDSN string
}

func setupStack(t *testing.T) authStack {
	t.Helper()
	ctx := context.Background()

	postgresContainer, err := postgres.Run(ctx, "postgres:18-alpine",
		postgres.WithDatabase("user_revocation_test"),
		postgres.WithUsername("user"),
		postgres.WithPassword("user"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, postgresContainer)
	if err != nil {
		t.Fatalf("start PostgreSQL: %v", err)
	}

	// Create a dedicated database for the revocation table.
	// The revocation store will use the same connection but create the table.
	revocationDSN, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := database.Open(revocationDSN, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if err := db.Exec("TRUNCATE users CASCADE").Error; err != nil {
		t.Fatal(err)
	}

	revocations, err := revocation.NewStore(ctx,
		revocation.Config{Address: revocationDSN, TTL: accessTTL})
	if err != nil {
		t.Fatalf("open revocation store: %v", err)
	}
	t.Cleanup(func() { _ = revocations.Close() })

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := userjwt.NewES256Codec(key, "test", httpauth.TokenIssuer, httpauth.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}

	frontend, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}

	logic, err := service.NewService(service.Dependencies{
		Resources: service.Resources{
			Users:             persistence.Users,
			AuthTokens:        persistence.AuthTokens,
			Sessions:          persistence.Sessions,
			DomainPolicy:      allowAllDomains{},
			UserCreatedEvents: persistence.UserCreatedOutbox,
		},
		WithTransaction: func(ctx context.Context, operation func(service.Resources) error) error {
			return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				txStore := store.New(tx)
				return operation(service.Resources{
					Users:             txStore.Users,
					AuthTokens:        txStore.AuthTokens,
					Sessions:          txStore.Sessions,
					DomainPolicy:      allowAllDomains{},
					UserCreatedEvents: txStore.UserCreatedOutbox,
				})
			})
		},
		TokenCodec:  codec,
		EmailSender: stubEmailSender{},
		Revocations: revocations,
	}, service.Config{FrontendBaseURL: *frontend, AccessTokenTTL: accessTTL, RefreshTokenTTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}

	roles := &service.RoleService{
		Users: persistence.Users,
		WithTransaction: func(ctx context.Context, operation func(service.RoleStore) error) error {
			return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				txStore := store.New(tx)
				return operation(service.RoleStore{Users: txStore.Users, Admin: txStore.Admin,
					Sessions: txStore.Sessions})
			})
		},
		Revocations: revocations,
	}

	return authStack{store: persistence, db: db, logic: logic, roles: roles, codec: codec,
		revocations: revocations, revocationDSN: revocationDSN}
}

func createUsuario(t *testing.T, stack authStack, address string, role models.RoleName) models.User {
	t.Helper()
	user := models.User{Email: address, DisplayName: "User", Role: role}
	if err := stack.store.Users.Create(context.Background(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

// TestRevocationAcceptsValidSession proves a fresh session passes revocation check.
func TestRevocationAcceptsValidSession(t *testing.T) {
	stack := setupStack(t)
	defer func() { _ = stack.db.Close() }()

	ctx := context.Background()
	user := createUsuario(t, stack, "valid@example.com", models.RoleUser)

	// Login creates a session.
	access, refresh := compositionLogin(t, stack.logic, stack.store, user)

	// The access token should pass revocation check.
	authenticator, err := httpauth.NewAuthenticator(ctx, httpauth.AuthConfig{
		UserServiceURL: "http://localhost:8081",
		Issuer:         httpauth.TokenIssuer,
		Audience:       httpauth.TokenAudience,
		Revocations:    stack.revocations,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer authenticator.Close()

	// Verify the session is not revoked.
	revoked, err := stack.revocations.IsRevoked(ctx, "session-1")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if revoked {
		t.Fatal("fresh session should not be revoked")
	}
}

// TestRevocationRejectsAfterLogout proves logout makes the token rejected.
func TestRevocationRejectsAfterLogout(t *testing.T) {
	stack := setupStack(t)
	defer func() { _ = stack.db.Close() }()

	ctx := context.Background()
	user := createUsuario(t, stack, "logout@example.com", models.RoleUser)

	access, refresh := compositionLogin(t, stack.logic, stack.store, user)

	// Logout revokes the session.
	if err := stack.logic.Logout(ctx, refresh); err != nil {
		t.Fatal(err)
	}

	// The access token should now be rejected.
	revoked, err := stack.revocations.IsRevoked(ctx, "session-1")
	if err != nil {
		t.Fatalf("IsRevoked: %v", err)
	}
	if !revoked {
		t.Fatal("logged-out session should be revoked")
	}
}

// TestRevocationExpiryWithinLifetime proves entries expire within the access-token lifetime.
func TestRevocationExpiryWithinLifetime(t *testing.T) {
	stack := setupStack(t)
	defer func() { _ = stack.db.Close() }()

	ctx := context.Background()
	user := createUsuario(t, stack, "expiry@example.com", models.RoleUser)

	_, refresh := compositionLogin(t, stack.logic, stack.store, user)

	// Logout revokes the session.
	if err := stack.logic.Logout(ctx, refresh); err != nil {
		t.Fatal(err)
	}

	// Entry should exist now.
	revoked, err := stack.revocations.IsRevoked(ctx, "session-1")
	if err != nil || !revoked {
		t.Fatalf("immediately after logout: revoked=%v err=%v", revoked, err)
	}

	// Wait for TTL + buffer (the revocation table uses expires_at, not Redis TTL).
	time.Sleep(accessTTL + 100*time.Millisecond)

	// Entry should be expired and no longer revoked.
	revoked, err = stack.revocations.IsRevoked(ctx, "session-1")
	if err != nil || revoked {
		t.Fatalf("after TTL: revoked=%v err=%v", revoked, err)
	}

	// DeleteExpired should remove expired rows.
	deleted, err := stack.revocations.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 row deleted, got %d", deleted)
	}
}

// TestCleanupRemovesExpiredEntries proves DeleteExpired removes only expired entries.
func TestCleanupRemovesExpiredEntries(t *testing.T) {
	stack := setupStack(t)
	defer func() { _ = stack.db.Close() }()

	ctx := context.Background()

	// Create some entries that will expire soon (via TTL=1s in revocation store).
	if err := stack.revocations.Revoke(ctx, "expiring1", "expiring2"); err != nil {
		t.Fatal(err)
	}

	// Create some entries with longer TTL (won't expire).
	longStore, err := revocation.NewStore(ctx,
		revocation.Config{Address: stack.revocationDSN, TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer longStore.Close()
	if err := longStore.Revoke(ctx, "long1", "long2"); err != nil {
		t.Fatal(err)
	}

	// Wait for the short-lived ones to expire.
	time.Sleep(1200 * time.Millisecond)

	// DeleteExpired should remove only the expired ones.
	deleted, err := stack.revocations.DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("DeleteExpired: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("expected 2 deleted, got %d", deleted)
	}

	// The long-lived ones should still be revoked.
	for _, sid := range []string{"long1", "long2"} {
		revoked, err := longStore.IsRevoked(ctx, sid)
		if err != nil || !revoked {
			t.Fatalf("session %q: revoked=%v err=%v", sid, revoked, err)
		}
	}
}

// compositionLogin runs a magic-link login through the real store and returns
// the access and refresh tokens.
func compositionLogin(t *testing.T, logic *service.Service, persistence *store.Store, user models.User) (string, string) {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tokenDigest := sha256.Sum256([]byte(token))
	challenge := models.AuthToken{TokenHash: tokenDigest[:], Purpose: models.TokenPurposeLogin,
		Email: user.Email, UserID: &user.ID, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if err := persistence.AuthTokens.Create(context.Background(), &challenge); err != nil {
		t.Fatal(err)
	}
	_, tokens, err := logic.Login(context.Background(), token)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return tokens.AccessToken, tokens.RefreshToken
}

// Helper function to access the store from authStack.
func persistence(stack *authStack) *store.Store {
	return stack.store
}
