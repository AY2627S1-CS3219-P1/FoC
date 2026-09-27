package service

import (
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
)

type fakeStore struct {
	mu            sync.Mutex
	users         map[string]jwt.User
	logins        map[[32]byte]jwt.LoginChallenge
	registrations map[[32]byte]jwt.RegistrationChallenge
	sessions      map[string]jwt.Session
	failSession   bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{users: map[string]jwt.User{}, logins: map[[32]byte]jwt.LoginChallenge{},
		registrations: map[[32]byte]jwt.RegistrationChallenge{}, sessions: map[string]jwt.Session{}}
}

func (f *fakeStore) FindByEmail(_ context.Context, email string) (jwt.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, user := range f.users {
		if user.Email == email {
			return user, nil
		}
	}
	return jwt.User{}, jwt.ErrNotFound
}

func (f *fakeStore) FindByID(_ context.Context, id string) (jwt.User, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	user, ok := f.users[id]
	if !ok {
		return jwt.User{}, jwt.ErrNotFound
	}
	return user, nil
}

func (f *fakeStore) SaveLogin(_ context.Context, challenge jwt.LoginChallenge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins[challenge.Digest] = challenge
	return nil
}

func (f *fakeStore) CompleteLogin(_ context.Context, digest [32]byte, now time.Time, factory SessionFactory) (jwt.User, jwt.AuthTokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	challenge, ok := f.logins[digest]
	if !ok || !now.Before(challenge.ExpiresAt) {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrChallengeRejected
	}
	user, ok := f.users[challenge.UserID]
	if !ok {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrNotFound
	}
	session, tokens, err := factory(user)
	if err != nil {
		return jwt.User{}, jwt.AuthTokens{}, err
	}
	if f.failSession {
		return jwt.User{}, jwt.AuthTokens{}, errors.New("session storage failed")
	}
	f.sessions[session.ID] = session
	delete(f.logins, digest)
	return user, tokens, nil
}

func (f *fakeStore) SaveRegistration(_ context.Context, challenge jwt.RegistrationChallenge) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registrations[challenge.Digest] = challenge
	return nil
}

func (f *fakeStore) Complete(_ context.Context, digest [32]byte, profile jwt.Profile, now time.Time, factory SessionFactory) (jwt.User, jwt.AuthTokens, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	challenge, ok := f.registrations[digest]
	if !ok || !now.Before(challenge.ExpiresAt) {
		return jwt.User{}, jwt.AuthTokens{}, jwt.ErrChallengeRejected
	}
	for _, user := range f.users {
		if user.Email == challenge.Email {
			return jwt.User{}, jwt.AuthTokens{}, jwt.ErrAlreadyRegistered
		}
	}
	user := jwt.User{ID: challenge.Email, Email: challenge.Email, DisplayName: profile.DisplayName, Role: jwt.RoleUser}
	session, tokens, err := factory(user)
	if err != nil {
		return jwt.User{}, jwt.AuthTokens{}, err
	}
	if f.failSession {
		return jwt.User{}, jwt.AuthTokens{}, errors.New("session storage failed")
	}
	f.users[user.ID] = user
	f.sessions[session.ID] = session
	delete(f.registrations, digest)
	return user, tokens, nil
}

func (f *fakeStore) Rotate(_ context.Context, id string, oldDigest, newDigest [32]byte, now, expiry time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || !now.Before(session.ExpiresAt) || session.RefreshDigest != oldDigest {
		return jwt.ErrSessionRejected
	}
	session.RefreshDigest = newDigest
	session.ExpiresAt = expiry
	f.sessions[id] = session
	return nil
}

func (f *fakeStore) Revoke(_ context.Context, id string, digest [32]byte, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	session, ok := f.sessions[id]
	if !ok || !now.Before(session.ExpiresAt) || session.RefreshDigest != digest {
		return jwt.ErrSessionRejected
	}
	delete(f.sessions, id)
	return nil
}

type loginStoreAdapter struct{ *fakeStore }

func (a loginStoreAdapter) Save(ctx context.Context, c jwt.LoginChallenge) error {
	return a.SaveLogin(ctx, c)
}

func (a loginStoreAdapter) Complete(ctx context.Context, digest [32]byte, now time.Time, factory SessionFactory) (jwt.User, jwt.AuthTokens, error) {
	return a.CompleteLogin(ctx, digest, now, factory)
}

type registrationStoreAdapter struct{ *fakeStore }

func (a registrationStoreAdapter) Save(ctx context.Context, c jwt.RegistrationChallenge) error {
	return a.SaveRegistration(ctx, c)
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
	service, err := NewService(Dependencies{Users: store, LoginTokens: loginStoreAdapter{store},
		RegistrationTokens: registrationStoreAdapter{store}, Sessions: store, TokenCodec: codec, EmailSender: sender},
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
		t.Fatalf("refresh rotation: %+v, %v", rotated, err)
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
	store.users["u1"] = jwt.User{ID: "u1", Email: "user@example.com", Role: jwt.RoleUser}
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
	store.users["u1"] = jwt.User{ID: "u1", Email: "user@example.com", Role: jwt.RoleUser}
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
	store.users["u1"] = jwt.User{ID: "u1", Email: "user@example.com", Role: jwt.RoleSuspendedUser}
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
	user := store.users["u1"]
	user.Role = jwt.RoleUser
	store.users["u1"] = user
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
	t.Run("user store", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.Users = nil
		assertPanics(t, func() { _ = service.RequestLink(ctx, "new@example.com") })
	})
	t.Run("login token store while requesting link", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		_ = addLoginChallenge(t, store, now)
		service.deps.LoginTokens = nil
		assertPanics(t, func() { _ = service.RequestLink(ctx, "user@example.com") })
	})
	t.Run("registration token store while requesting link", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.RegistrationTokens = nil
		assertPanics(t, func() { _ = service.RequestLink(ctx, "new@example.com") })
	})
	t.Run("login token store while completing login", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addLoginChallenge(t, store, now)
		service.deps.LoginTokens = nil
		assertPanics(t, func() { _, _, _ = service.Login(ctx, token) })
	})
	t.Run("token codec while completing login", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addLoginChallenge(t, store, now)
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _, _, _ = service.Login(ctx, token) })
	})
	t.Run("registration token store while completing registration", func(t *testing.T) {
		store := newFakeStore()
		service := newService(t, store)
		token := addRegistrationChallenge(t, store, now)
		service.deps.RegistrationTokens = nil
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
	t.Run("user store while refreshing", func(t *testing.T) {
		service, _, refreshToken := serviceWithRefreshToken(t, now)
		service.deps.Users = nil
		assertPanics(t, func() { _, _ = service.Refresh(ctx, refreshToken) })
	})
	t.Run("session store while refreshing", func(t *testing.T) {
		service, _, refreshToken := serviceWithRefreshToken(t, now)
		service.deps.Sessions = nil
		assertPanics(t, func() { _, _ = service.Refresh(ctx, refreshToken) })
	})
	t.Run("token codec while logging out", func(t *testing.T) {
		service := newService(t, newFakeStore())
		service.deps.TokenCodec = nil
		assertPanics(t, func() { _ = service.Logout(ctx, "refresh") })
	})
	t.Run("session store while logging out", func(t *testing.T) {
		service, _, refreshToken := serviceWithRefreshToken(t, now)
		service.deps.Sessions = nil
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
	user := jwt.User{ID: "user-id", Email: "user@example.com", DisplayName: "User", Role: jwt.RoleUser}
	store.users[user.ID] = user
	store.logins[digest] = jwt.LoginChallenge{Digest: digest, UserID: user.ID, ExpiresAt: now.Add(MagicLinkLifetime)}
	return token
}

func addRegistrationChallenge(t *testing.T, store *fakeStore, now time.Time) string {
	t.Helper()
	token, digest, err := randomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	store.registrations[digest] = jwt.RegistrationChallenge{
		Digest: digest, Email: "new@example.com", ExpiresAt: now.Add(MagicLinkLifetime),
	}
	return token
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
