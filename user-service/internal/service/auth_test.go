package service

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/email"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	storepkg "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type fakeStore struct {
	mu             sync.Mutex
	users          map[uuid.UUID]models.User
	logins         map[[32]byte]models.AuthToken
	registrations  map[[32]byte]models.AuthToken
	sessions       map[uuid.UUID]models.Session
	allowedDomains map[string]struct{}
	failSession    bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[uuid.UUID]models.User{}, logins: map[[32]byte]models.AuthToken{},
		registrations: map[[32]byte]models.AuthToken{}, sessions: map[uuid.UUID]models.Session{},
		allowedDomains: map[string]struct{}{"example.com": {}}}
}

func (f *fakeStore) GetByEmail(_ context.Context, email string) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, user := range f.users {
		if user.Email == email {
			found := user
			return &found, nil
		}
	}
	return nil, storepkg.ErrNotFound
}

func (f *fakeStore) GetByID(_ context.Context, id uuid.UUID) (*models.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.users[id]
	if !ok {
		return nil, storepkg.ErrNotFound
	}
	return &user, nil
}

func (f *fakeStore) CreateUser(_ context.Context, user *models.User) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, existing := range f.users {
		if existing.Email == user.Email {
			return storepkg.ErrDuplicate
		}
	}
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	f.users[user.ID] = *user
	return nil
}

func (f *fakeStore) CreateToken(_ context.Context, challenge *models.AuthToken) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	digest := bytesToDigest(challenge.TokenHash)
	switch challenge.Purpose {
	case models.TokenPurposeLogin:
		f.logins[digest] = *challenge
	case models.TokenPurposeRegister:
		f.registrations[digest] = *challenge
	default:
		return errors.New("invalid token purpose")
	}
	return nil
}

func (f *fakeStore) ConsumeToken(_ context.Context, digest [32]byte, purpose models.TokenPurpose, now time.Time) (*models.AuthToken, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	challenges := f.registrations
	if purpose == models.TokenPurposeLogin {
		challenges = f.logins
	}
	challenge, ok := challenges[digest]
	if !ok || !now.Before(challenge.ExpiresAt) {
		return nil, storepkg.ErrChallengeRejected
	}
	delete(challenges, digest)
	return &challenge, nil
}

func (f *fakeStore) CreateSession(_ context.Context, session *models.Session) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSession {
		return errors.New("session storage failed")
	}
	if _, exists := f.sessions[session.ID]; exists {
		return storepkg.ErrDuplicate
	}
	f.sessions[session.ID] = *session
	return nil
}

func (f *fakeStore) RevokeSession(_ context.Context, id uuid.UUID, digest [32]byte, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || session.RevokedAt != nil || !now.Before(session.ExpiresAt) || !bytes.Equal(session.TokenHash, digest[:]) {
		return storepkg.ErrSessionRejected
	}
	revokedAt := now
	session.RevokedAt = &revokedAt
	f.sessions[id] = session
	return nil
}

type fakeUsers struct{ *fakeStore }

func (f fakeUsers) Create(ctx context.Context, user *models.User) error {
	return f.CreateUser(ctx, user)
}

type fakeAuthTokens struct{ *fakeStore }

func (f fakeAuthTokens) Create(ctx context.Context, token *models.AuthToken) error {
	return f.CreateToken(ctx, token)
}

func (f fakeAuthTokens) Consume(ctx context.Context, digest [32]byte, purpose models.TokenPurpose, now time.Time) (*models.AuthToken, error) {
	return f.ConsumeToken(ctx, digest, purpose, now)
}

type fakeSessions struct{ *fakeStore }

func (f fakeSessions) Create(ctx context.Context, session *models.Session) error {
	return f.CreateSession(ctx, session)
}

func (f fakeSessions) Revoke(ctx context.Context, id uuid.UUID, digest [32]byte, now time.Time) error {
	return f.RevokeSession(ctx, id, digest, now)
}

type fakeDomains struct{ *fakeStore }

func (f fakeDomains) Allows(_ context.Context, domain string) (bool, error) {
	_, ok := f.allowedDomains[strings.ToLower(domain)]
	return ok, nil
}

func (f *fakeStore) stores() Store {
	return Store{
		Users:      fakeUsers{f},
		AuthTokens: fakeAuthTokens{f},
		Sessions:   fakeSessions{f},
		Domains:    fakeDomains{f},
	}
}

func (f *fakeStore) withTransaction(ctx context.Context, operation func(Store) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	tx := &fakeStore{
		users:          cloneMap(f.users),
		logins:         cloneMap(f.logins),
		registrations:  cloneMap(f.registrations),
		sessions:       cloneMap(f.sessions),
		allowedDomains: cloneMap(f.allowedDomains),
		failSession:    f.failSession,
	}
	if err := operation(tx.stores()); err != nil {
		return err
	}
	f.users = tx.users
	f.logins = tx.logins
	f.registrations = tx.registrations
	f.sessions = tx.sessions
	f.allowedDomains = tx.allowedDomains
	return nil
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	clone := make(map[K]V, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func bytesToDigest(value []byte) [32]byte {
	var digest [32]byte
	copy(digest[:], value)
	return digest
}

type mockEmailSender struct{ messages []email.Email }

func (e *mockEmailSender) Send(_ context.Context, message email.Email) error {
	e.messages = append(e.messages, message)
	return nil
}

func setupService(t *testing.T, store *fakeStore, clock *time.Time, dev bool, sender email.EmailSender) *Service {
	t.Helper()
	if sender == nil {
		sender = &mockEmailSender{}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := jwt.NewES256Codec(key, "test", "foc-user-service", "foc-services")
	if err != nil {
		t.Fatal(err)
	}
	base := "https://example.test"
	if dev {
		base = "http://localhost:5173"
	}
	frontendURL, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(Dependencies{Store: store.stores(), WithTransaction: store.withTransaction,
		TokenCodec: codec, EmailSender: sender},
		Config{FrontendBaseURL: *frontendURL, LocalDevelopment: dev,
			Now: func() time.Time { return *clock }})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func requestLink(t *testing.T, service *Service, email string) string {
	t.Helper()
	sender := service.deps.EmailSender.(*mockEmailSender)
	before := len(sender.messages)
	if err := service.RequestLink(context.Background(), email); err != nil {
		t.Fatal(err)
	}
	if len(sender.messages) != before+1 {
		t.Fatal("magic link was not sent")
	}
	return strings.TrimSpace(strings.TrimPrefix(sender.messages[before].TextBody,
		"Use this link to sign in or create an account:"))
}

func linkToken(t *testing.T, link string) string {
	t.Helper()
	parsed, err := url.Parse(link)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.Query().Get("token")
}

func TestRegistrationAndRefreshLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	service := setupService(t, store, &now, true, nil)
	ctx := context.Background()

	link := requestLink(t, service, " NEW@Example.com ")
	if !strings.Contains(link, "/register?") {
		t.Fatalf("registration link: %s", link)
	}
	if len(store.users) != 0 {
		t.Fatal("user was created before verification")
	}
	token := linkToken(t, link)
	if _, _, err := service.Register(ctx, "not-a-token", jwt.Profile{DisplayName: "New"}); !errors.Is(err, jwt.ErrRegistrationFailed) {
		t.Fatalf("malformed registration token: %v", err)
	}
	user, session, err := service.Register(ctx, token, jwt.Profile{DisplayName: " New "})
	if err != nil || user.Email != "new@example.com" || user.DisplayName != "New" || session.AccessToken == "" {
		t.Fatalf("registration: %+v, %+v, %v", user, session, err)
	}
	if _, _, err := service.Register(ctx, token, jwt.Profile{DisplayName: "New"}); !errors.Is(err, jwt.ErrRegistrationFailed) {
		t.Fatalf("reused registration token: %v", err)
	}
	oldRefresh := session.RefreshToken
	rotated, err := service.Refresh(ctx, oldRefresh)
	if err != nil || rotated.RefreshToken == oldRefresh {
		t.Fatalf("refresh replacement: %+v, %v", rotated, err)
	}
	oldClaims, err := service.deps.TokenCodec.Verify(oldRefresh, jwt.RefreshToken, now)
	if err != nil {
		t.Fatal(err)
	}
	newClaims, err := service.deps.TokenCodec.Verify(rotated.RefreshToken, jwt.RefreshToken, now)
	if err != nil || newClaims.SessionID == oldClaims.SessionID {
		t.Fatalf("refresh reused session ID: old=%q new=%q err=%v", oldClaims.SessionID, newClaims.SessionID, err)
	}
	if _, err := service.Refresh(ctx, oldRefresh); !errors.Is(err, jwt.ErrRefreshFailed) {
		t.Fatalf("reused refresh token: %v", err)
	}
	if err := service.Logout(ctx, rotated.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Refresh(ctx, rotated.RefreshToken); !errors.Is(err, jwt.ErrRefreshFailed) {
		t.Fatalf("refresh after logout: %v", err)
	}
	if err := service.Logout(ctx, rotated.RefreshToken); err != nil {
		t.Fatalf("idempotent logout: %v", err)
	}
}

func TestIndependentLoginLinksAndExpiry(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	user := testModelUser("user@example.com", models.RoleUser)
	store.users[user.ID] = user
	service := setupService(t, store, &now, true, nil)
	ctx := context.Background()
	first := requestLink(t, service, "user@example.com")
	second := requestLink(t, service, "user@example.com")
	if first == second {
		t.Fatal("links were not independent")
	}
	for _, link := range []string{first, second} {
		if _, _, err := service.Login(ctx, linkToken(t, link)); err != nil {
			t.Fatalf("valid login link rejected: %v", err)
		}
	}
	if _, _, err := service.Login(ctx, linkToken(t, first)); !errors.Is(err, jwt.ErrLoginFailed) {
		t.Fatalf("reused login link: %v", err)
	}
	expiring := requestLink(t, service, "user@example.com")
	now = now.Add(MagicLinkLifetime)
	if _, _, err := service.Login(ctx, linkToken(t, expiring)); !errors.Is(err, jwt.ErrLoginFailed) {
		t.Fatalf("expired login link: %v", err)
	}
}

func TestConcurrentLoginConsumesOnce(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	user := testModelUser("user@example.com", models.RoleUser)
	store.users[user.ID] = user
	service := setupService(t, store, &now, true, nil)
	link := requestLink(t, service, "user@example.com")
	token := linkToken(t, link)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := service.Login(context.Background(), token)
			results <- err
		}()
	}
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, jwt.ErrLoginFailed)) ||
		(second == nil && errors.Is(first, jwt.ErrLoginFailed))) {
		t.Fatalf("concurrent results: %v, %v", first, second)
	}
}

func TestSuspendedUserCanAuthenticateAndRefreshRole(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	user := testModelUser("user@example.com", models.RoleSuspended)
	store.users[user.ID] = user
	service := setupService(t, store, &now, false, &mockEmailSender{})
	link := requestLink(t, service, "user@example.com")
	_, tokens, err := service.Login(context.Background(), linkToken(t, link))
	if err != nil {
		t.Fatalf("suspended user cannot sign in for history and appeals: %v", err)
	}
	claims, err := service.deps.TokenCodec.Verify(tokens.AccessToken, jwt.AccessToken, now)
	if err != nil || claims.Role != jwt.RoleSuspendedUser {
		t.Fatalf("access token lost suspended role: %+v, %v", claims, err)
	}
	tokens, err = service.Refresh(context.Background(), tokens.RefreshToken)
	if err != nil {
		t.Fatalf("suspended user cannot refresh: %v", err)
	}
	user.Role = models.RoleUser
	store.users[user.ID] = user
	tokens, err = service.Refresh(context.Background(), tokens.RefreshToken)
	if err != nil {
		t.Fatal(err)
	}
	claims, err = service.deps.TokenCodec.Verify(tokens.AccessToken, jwt.AccessToken, now)
	if err != nil || claims.Role != jwt.RoleUser {
		t.Fatalf("refresh did not use persisted role: %+v, %v", claims, err)
	}
}

func TestProductionLinkIsEmailOnly(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	sender := &mockEmailSender{}
	service := setupService(t, store, &now, false, sender)
	if err := service.RequestLink(context.Background(), "user@example.com"); err != nil || len(sender.messages) != 1 {
		t.Fatalf("production email failed: %v", err)
	}
	digest := sha256.Sum256([]byte(linkToken(t, strings.TrimSpace(strings.TrimPrefix(sender.messages[0].TextBody,
		"Use this link to sign in or create an account:")))))
	if _, ok := store.registrations[digest]; !ok {
		t.Fatal("emailed token digest was not stored")
	}
	if _, err := normalizeEmail("Name <user@example.com>"); !errors.Is(err, jwt.ErrInvalidEmail) {
		t.Fatalf("display-name email accepted: %v", err)
	}
	localURL, err := url.Parse("http://localhost:5173")
	if err != nil {
		t.Fatal(err)
	}
	localEmailOnly, err := NewService(service.deps, Config{FrontendBaseURL: *localURL,
		LocalDevelopment: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatalf("local email-only mode rejected: %v", err)
	}
	if err := localEmailOnly.RequestLink(context.Background(), "another@example.com"); err != nil || len(sender.messages) != 2 {
		t.Fatalf("local email mode: %v", err)
	}
}

func TestInvalidProfileAndDuplicateRegistration(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	service := setupService(t, store, &now, true, nil)
	ctx := context.Background()
	if err := service.RequestLink(ctx, "bad-address"); !errors.Is(err, jwt.ErrInvalidEmail) {
		t.Fatalf("invalid email: %v", err)
	}
	first := requestLink(t, service, "new@example.com")
	second := requestLink(t, service, "new@example.com")
	if _, _, err := service.Register(ctx, linkToken(t, first), jwt.Profile{DisplayName: " "}); !errors.Is(err, jwt.ErrInvalidProfile) {
		t.Fatalf("invalid profile: %v", err)
	}
	if _, _, err := service.Register(ctx, linkToken(t, first), jwt.Profile{DisplayName: "New"}); err != nil {
		t.Fatalf("valid link after invalid profile: %v", err)
	}
	if _, _, err := service.Register(ctx, linkToken(t, second), jwt.Profile{DisplayName: "Again"}); !errors.Is(err, jwt.ErrAlreadyRegistered) {
		t.Fatalf("duplicate registration: %v", err)
	}
}

func TestRegistrationRequiresAllowedDomain(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	store.allowedDomains = map[string]struct{}{"u.nus.edu": {}}
	service := setupService(t, store, &now, true, nil)
	ctx := context.Background()
	token := addRegistrationChallenge(t, store, now)

	if _, _, err := service.Register(ctx, token, jwt.Profile{DisplayName: "New"}); !errors.Is(err, jwt.ErrRegistrationFailed) {
		t.Fatalf("registration with a disallowed domain: %v", err)
	}
	if len(store.users) != 0 {
		t.Fatal("disallowed registration created a user")
	}
	if _, ok := store.registrations[sha256.Sum256([]byte(token))]; !ok {
		t.Fatal("disallowed registration consumed its token")
	}

	store.allowedDomains["example.com"] = struct{}{}
	if _, _, err := service.Register(ctx, token, jwt.Profile{DisplayName: "New"}); err != nil {
		t.Fatalf("registration after allowing the domain: %v", err)
	}
}

func TestSessionStorageFailurePreservesMagicLink(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	store := newFakeStore()
	service := setupService(t, store, &now, true, nil)
	ctx := context.Background()
	registration := requestLink(t, service, "new@example.com")
	store.failSession = true
	if _, _, err := service.Register(ctx, linkToken(t, registration), jwt.Profile{DisplayName: "New"}); err == nil {
		t.Fatal("registration succeeded despite session storage failure")
	}
	if len(store.users) != 0 {
		t.Fatal("failed registration created a user")
	}
	store.failSession = false
	if _, _, err := service.Register(ctx, linkToken(t, registration), jwt.Profile{DisplayName: "New"}); err != nil {
		t.Fatalf("registration link lost after storage failure: %v", err)
	}
	login := requestLink(t, service, "new@example.com")
	store.failSession = true
	if _, _, err := service.Login(ctx, linkToken(t, login)); err == nil {
		t.Fatal("login succeeded despite session storage failure")
	}
	store.failSession = false
	if _, _, err := service.Login(ctx, linkToken(t, login)); err != nil {
		t.Fatalf("login link lost after storage failure: %v", err)
	}
}

func TestSessionStorageFailurePreservesRefreshSession(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	service, store, refreshToken := serviceWithRefreshToken(t, now)
	store.failSession = true
	if _, err := service.Refresh(context.Background(), refreshToken); err == nil {
		t.Fatal("refresh succeeded despite replacement session storage failure")
	}
	store.failSession = false
	if _, err := service.Refresh(context.Background(), refreshToken); err != nil {
		t.Fatalf("failed refresh revoked the original session: %v", err)
	}
}

func TestConcurrentRefreshReplacesSessionOnce(t *testing.T) {
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	service, _, refreshToken := serviceWithRefreshToken(t, now)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, err := service.Refresh(context.Background(), refreshToken)
			results <- err
		}()
	}
	first, second := <-results, <-results
	if !((first == nil && errors.Is(second, jwt.ErrRefreshFailed)) ||
		(second == nil && errors.Is(first, jwt.ErrRefreshFailed))) {
		t.Fatalf("concurrent refresh results: %v, %v", first, second)
	}
}

func TestProductionRequiresHTTPSFrontend(t *testing.T) {
	frontendURL, err := url.Parse("http://example.test")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewService(Dependencies{}, Config{FrontendBaseURL: *frontendURL}); err == nil {
		t.Fatal("production configuration allowed an HTTP frontend")
	}
}

func TestMissingAuthDependenciesPanic(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)
	newService := func(t *testing.T, store *fakeStore) *Service {
		t.Helper()
		return setupService(t, store, &now, true, nil)
	}

	t.Run("email sender", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.EmailSender = nil
		assertPanics(t, func() { _ = service.RequestLink(ctx, "new@example.com") })
	})
	t.Run("auth store while requesting link", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.Store.Users = nil
		assertPanics(t, func() { _ = service.RequestLink(ctx, "new@example.com") })
	})
	t.Run("transaction runner while completing login", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addLoginChallenge(t, store, now)
		service.deps.WithTransaction = nil
		assertPanics(t, func() { _, _, _ = service.Login(ctx, token) })
	})
	t.Run("token codec while completing login", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addLoginChallenge(t, store, now)
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _, _, _ = service.Login(ctx, token) })
	})
	t.Run("transaction runner while completing registration", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addRegistrationChallenge(t, store, now)
		service.deps.WithTransaction = nil
		assertPanics(t, func() { _, _, _ = service.Register(ctx, token, jwt.Profile{DisplayName: "New"}) })
	})
	t.Run("token codec while completing registration", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addRegistrationChallenge(t, store, now)
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _, _, _ = service.Register(ctx, token, jwt.Profile{DisplayName: "New"}) })
	})
	t.Run("token codec while refreshing", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _, _ = service.Refresh(ctx, "refresh") })
	})
	t.Run("auth store while refreshing", func(t *testing.T) {
		service, _, refreshToken := serviceWithRefreshToken(t, now)
		service.deps.Store.Users = nil
		assertPanics(t, func() { _, _ = service.Refresh(ctx, refreshToken) })
	})
	t.Run("transaction runner while refreshing", func(t *testing.T) {
		service, _, refreshToken := serviceWithRefreshToken(t, now)
		service.deps.WithTransaction = nil
		assertPanics(t, func() { _, _ = service.Refresh(ctx, refreshToken) })
	})
	t.Run("token codec while logging out", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _ = service.Logout(ctx, "refresh") })
	})
	t.Run("auth store while logging out", func(t *testing.T) {
		service, _, refreshToken := serviceWithRefreshToken(t, now)
		service.deps.Store.Sessions = nil
		assertPanics(t, func() { _ = service.Logout(ctx, refreshToken) })
	})
	t.Run("token codec while reading public keys", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _, _ = service.PublicKeys() })
	})
}

func addLoginChallenge(t *testing.T, store *fakeStore, now time.Time) string {
	t.Helper()
	token, digest, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	user := testModelUser("user@example.com", models.RoleUser)
	store.users[user.ID] = user
	store.logins[digest] = models.AuthToken{TokenHash: append([]byte(nil), digest[:]...), Purpose: models.TokenPurposeLogin,
		Email: user.Email, UserID: &user.ID, ExpiresAt: now.Add(MagicLinkLifetime)}
	return token
}

func addRegistrationChallenge(t *testing.T, store *fakeStore, now time.Time) string {
	t.Helper()
	token, digest, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	store.registrations[digest] = models.AuthToken{
		TokenHash: append([]byte(nil), digest[:]...), Purpose: models.TokenPurposeRegister,
		Email: "new@example.com", ExpiresAt: now.Add(MagicLinkLifetime),
	}
	return token
}

func testModelUser(email string, role models.RoleName) models.User {
	return models.User{ID: uuid.New(), Email: email, DisplayName: "User", Role: role}
}

func serviceWithRefreshToken(t *testing.T, now time.Time) (*Service, *fakeStore, string) {
	t.Helper()
	store := newFakeStore()
	clock := now
	service := setupService(t, store, &clock, true, nil)
	token := addLoginChallenge(t, store, now)
	_, tokens, err := service.Login(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	return service, store, tokens.RefreshToken
}

func assertPanics(t *testing.T, call func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatal("expected nil dependency to panic")
		}
	}()
	call()
}
