package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/revocation"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/bootstrap"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	userservicejwt "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"gorm.io/gorm"
)

const compositionAccessTTL = time.Minute

// setupComposition opens the shared throwaway database, a miniredis, and the
// production auth stack exactly as main.go wires it.
func setupComposition(t *testing.T) (
	logic authhandler.Logic, persistence *store.Store, codec *userservicejwt.ES256Codec,
	db *gorm.DB, redisServer *miniredis.Miniredis,
) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	var err error
	db, err = database.Open(dsn, 5, 5)
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
	if err := db.Exec("TRUNCATE admin_bootstrap").Error; err != nil {
		t.Fatal(err)
	}
	// The transaction-bound domain policy reads the real table.
	if err := db.Exec(
		"INSERT INTO allowed_email_domains (domain) VALUES ('example.com')",
	).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DELETE FROM allowed_email_domains WHERE domain = 'example.com'").Error; err != nil {
			t.Error(err)
		}
	})

	redisServer = miniredis.RunT(t)
	frontendURL, err := url.Parse("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	revocations, err := revocation.NewStore(context.Background(),
		revocation.Config{Addr: redisServer.Addr(), TTL: compositionAccessTTL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = revocations.Close() })

	handler, codec, persistence, err := newAuthServices(db, config{
		frontendURL:     frontendURL.String(),
		privateKey:      writeSigningKey(t),
		accessTokenTTL:  compositionAccessTTL,
		refreshTokenTTL: time.Hour,
	}, allowEveryDomain{}, revocations)
	if err != nil {
		t.Fatal(err)
	}
	return handler.Logic, persistence, codec, db, redisServer
}

type allowEveryDomain struct{}

func (allowEveryDomain) IsDomainAllowed(context.Context, string) (bool, error) { return true, nil }

// writeSigningKey generates a P-256 key in the PKCS#8 PEM format main loads.
func writeSigningKey(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "jwt-signing-private.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func createCompositionUser(t *testing.T, persistence *store.Store, email string, role models.RoleName) models.User {
	t.Helper()
	user := models.User{Email: email, DisplayName: "User", Role: role}
	if err := persistence.Users.Create(context.Background(), &user); err != nil {
		t.Fatal(err)
	}
	return user
}

// compositionLogin runs a magic-link login through the real store and returns
// the access and refresh tokens.
func compositionLogin(t *testing.T, logic authhandler.Logic, persistence *store.Store, user models.User) (
	string, string,
) {
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

// refreshSessionID resolves the session a refresh token belongs to.
func refreshSessionID(t *testing.T, codec *userservicejwt.ES256Codec, refreshToken string) string {
	t.Helper()
	claims, err := codec.Verify(refreshToken, userservicejwt.RefreshToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return claims.SessionID
}

// assertCompositionRevoked checks the dictionary entry exists with a TTL
// within the access-token lifetime.
func assertCompositionRevoked(
	t *testing.T, redisServer *miniredis.Miniredis, sessionID string,
) {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	if !redisServer.Exists(revocation.Key(sessionID)) {
		t.Fatalf("session %q was not recorded in the revocation dictionary", sessionID)
	}
	ttl, err := client.TTL(context.Background(), revocation.Key(sessionID)).Result()
	if err != nil {
		t.Fatal(err)
	}
	if ttl <= 0 || ttl > compositionAccessTTL {
		t.Fatalf("entry TTL=%v, want within (0, %v]", ttl, compositionAccessTTL)
	}
}

// TestCompositionWiresRevocationDictionary proves the production stack records
// revoked sessions: a nil Revocations dependency would panic here right after
// the logout and refresh transactions commit.
func TestCompositionWiresRevocationDictionary(t *testing.T) {
	logic, persistence, codec, _, redisServer := setupComposition(t)
	ctx := context.Background()
	user := createCompositionUser(t, persistence, "wiring@example.com", models.RoleUser)

	_, refresh := compositionLogin(t, logic, persistence, user)
	if err := logic.Logout(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	assertCompositionRevoked(t, redisServer, refreshSessionID(t, codec, refresh))

	// Refresh rotation must also record the replaced session.
	_, refresh = compositionLogin(t, logic, persistence, user)
	if _, _, err := logic.Refresh(ctx, refresh); err != nil {
		t.Fatal(err)
	}
	assertCompositionRevoked(t, redisServer, refreshSessionID(t, codec, refresh))
}

// TestBootstrapPromotionRevokesSessions proves the bootstrap promotion signs
// the promoted user's existing sessions out of every device.
func TestBootstrapPromotionRevokesSessions(t *testing.T) {
	_, persistence, _, db, redisServer := setupComposition(t)
	ctx := context.Background()
	user := createCompositionUser(t, persistence, "promoted@example.com", models.RoleUser)

	sessionHash := sha256.Sum256([]byte("stale-device-hash"))
	session := models.Session{UserID: user.ID, TokenHash: sessionHash[:],
		LastSeenAt: time.Now().UTC(), ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := persistence.Sessions.Create(ctx, &session); err != nil {
		t.Fatal(err)
	}

	admin, outcome, err := (&bootstrap.Bootstrapper{Store: store.NewAdmin(db)}).Run(ctx,
		bootstrap.Config{Email: user.Email, DisplayName: "Promoted"})
	if err != nil {
		t.Fatal(err)
	}
	if outcome != store.BootstrapPromoted {
		t.Fatalf("bootstrap outcome = %v, want promotion", outcome)
	}
	revocations, err := revocation.NewStore(ctx,
		revocation.Config{Addr: redisServer.Addr(), TTL: compositionAccessTTL})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = revocations.Close() })
	if err := revokePromotedAdmin(ctx, db, revocations, admin); err != nil {
		t.Fatal(err)
	}

	var live int64
	if err := db.Model(&models.Session{}).
		Where("user_id = ? AND revoked_at IS NULL", user.ID).
		Count(&live).Error; err != nil || live != 0 {
		t.Fatalf("promoted user still has live sessions: count=%d err=%v", live, err)
	}
	assertCompositionRevoked(t, redisServer, session.ID.String())
}
