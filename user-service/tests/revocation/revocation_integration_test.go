//go:build integration

// Package revocation_test exercises the PostgreSQL-backed access-token revocation
// flow end to end: a live session is accepted, session or role revocation
// makes its still-cryptographically-valid access token rejected, and the
// entries expire within the access-token lifetime.
package revocation_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
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

	// One throwaway PostgreSQL hosts both the User Service tables and the
	// revocation table; the revocation store creates its schema on connect.
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
	databaseURL, err := postgresContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	db, err := database.Open(databaseURL, 5, 5)
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
		revocation.Config{Address: databaseURL, TTL: accessTTL})
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
	persistence := store.New(db)
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
		revocations: revocations, revocationDSN: databaseURL}
}

// createUser inserts a user and returns it.
func createUser(t *testing.T, stack authStack, address string, role models.RoleName) models.User {
	t.Helper()
	user := models.User{Email: address, DisplayName: "User", Role: role}
	if err := stack.store.Users.Create(context.Background(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

// login runs the magic-link login flow through the real store and returns the
// signed tokens.
func login(t *testing.T, stack authStack, user models.User) userjwt.AuthTokens {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	digest := sha256.Sum256([]byte(token))
	challenge := models.AuthToken{TokenHash: digest[:], Purpose: models.TokenPurposeLogin,
		Email: user.Email, UserID: &user.ID, ExpiresAt: time.Now().UTC().Add(10 * time.Minute)}
	if err := stack.store.AuthTokens.Create(context.Background(), &challenge); err != nil {
		t.Fatal(err)
	}
	_, tokens, err := stack.logic.Login(context.Background(), token)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	return tokens
}

// requestStatus runs the shared local authentication middleware exactly as
// main.go mounts it.
func requestStatus(t *testing.T, stack authStack, bearer string) int {
	t.Helper()
	handler := httpauth.AuthenticateLocal(stack.codec.VerifyAccess, stack.revocations)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+bearer)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response.Code
}

func TestLogoutRevokesOutstandingAccessToken(t *testing.T) {
	stack := setupStack(t)
	ctx := context.Background()
	user := createUser(t, stack, "logout@example.com", models.RoleUser)
	tokens := login(t, stack, user)

	if got := requestStatus(t, stack, tokens.AccessToken); got != http.StatusNoContent {
		t.Fatalf("live session: HTTP %d, want 204", got)
	}
	if err := stack.logic.Logout(ctx, tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}

	// The token still verifies cryptographically but its session is gone.
	if got := requestStatus(t, stack, tokens.AccessToken); got != http.StatusUnauthorized {
		t.Fatalf("after logout: HTTP %d, want 401", got)
	}
	if _, _, err := stack.logic.Refresh(ctx, tokens.RefreshToken); err == nil {
		t.Fatal("refresh after logout succeeded")
	}
}

// TestRevocationEntryExpiresWithinAccessTTL asserts the entry's expires_at lands
// inside the access-token lifetime and that an expired entry stops matching
// reads without needing the cleanup sweep.
func TestRevocationEntryExpiresWithinAccessTTL(t *testing.T) {
	stack := setupStack(t)
	ctx := context.Background()
	user := createUser(t, stack, "ttl@example.com", models.RoleUser)
	tokens := login(t, stack, user)

	claims, err := stack.codec.Verify(tokens.AccessToken, userjwt.AccessToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := pgxpool.ParseConfig(stack.revocationDSN)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	if err := stack.logic.Logout(ctx, tokens.RefreshToken); err != nil {
		t.Fatal(err)
	}
	var remaining time.Duration
	if err := pool.QueryRow(ctx,
		`SELECT expires_at - now() FROM revoked_access_tokens WHERE sid = $1`,
		claims.SessionID).Scan(&remaining); err != nil {
		t.Fatalf("query revocation entry: %v", err)
	}
	if remaining <= 0 || remaining > accessTTL {
		t.Fatalf("entry expires in %v, want within (0, %v]", remaining, accessTTL)
	}

	// Once the entry lapses the session stops being revoked and the sweep
	// reclaims the row.
	if _, err := pool.Exec(ctx,
		`UPDATE revoked_access_tokens SET expires_at = now() - interval '1 second' WHERE sid = $1`,
		claims.SessionID); err != nil {
		t.Fatal(err)
	}
	revoked, err := stack.revocations.IsRevoked(ctx, claims.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if revoked {
		t.Fatal("expired entry still reported as revoked")
	}
	deleted, err := stack.revocations.DeleteExpired(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("DeleteExpired removed %d rows, want 1", deleted)
	}
}

func TestRefreshRotationRevokesOldSessionTokens(t *testing.T) {
	stack := setupStack(t)
	ctx := context.Background()
	user := createUser(t, stack, "rotation@example.com", models.RoleUser)
	tokens := login(t, stack, user)

	_, rotated, err := stack.logic.Refresh(ctx, tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	if got := requestStatus(t, stack, tokens.AccessToken); got != http.StatusUnauthorized {
		t.Fatalf("old access token after rotation: HTTP %d, want 401", got)
	}
	if got := requestStatus(t, stack, rotated.AccessToken); got != http.StatusNoContent {
		t.Fatalf("new access token after rotation: HTTP %d, want 204", got)
	}
}

func TestRoleChangeRevokesTargetAccessTokens(t *testing.T) {
	stack := setupStack(t)
	ctx := context.Background()
	admin := createUser(t, stack, "admin@example.com", models.RoleSuperAdmin)
	target := createUser(t, stack, "target@example.com", models.RoleUser)
	tokens := login(t, stack, target)

	if _, err := stack.roles.ChangeUserRole(
		ctx,
		admin.ID,
		target.ID,
		models.RoleSuspended,
		"abuse",
	); err != nil {
		t.Fatal(err)
	}

	// Suspension signs the target out immediately, on every device.
	if got := requestStatus(t, stack, tokens.AccessToken); got != http.StatusUnauthorized {
		t.Fatalf("target access token after suspension: HTTP %d, want 401", got)
	}
	if _, _, err := stack.logic.Refresh(ctx, tokens.RefreshToken); err == nil {
		t.Fatal("target refreshed a suspended session")
	}
}

func TestLogoutAllRevokesEveryDevice(t *testing.T) {
	stack := setupStack(t)
	ctx := context.Background()
	user := createUser(t, stack, "everywhere@example.com", models.RoleUser)
	laptop := login(t, stack, user)
	phone := login(t, stack, user)

	if err := stack.logic.LogoutAll(ctx, laptop.RefreshToken); err != nil {
		t.Fatal(err)
	}
	for name, tokens := range map[string]userjwt.AuthTokens{"laptop": laptop, "phone": phone} {
		if got := requestStatus(t, stack, tokens.AccessToken); got != http.StatusUnauthorized {
			t.Fatalf("%s access token after logout all: HTTP %d, want 401", name, got)
		}
	}
}
