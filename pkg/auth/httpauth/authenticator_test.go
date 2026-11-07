package httpauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/golang-jwt/jwt/v5"
)

type publicKeyServiceStub struct {
	mu            sync.Mutex
	keys          []*userv1.JsonWebKey
	err           error
	requests      int
	blockRefresh  bool
	refreshOpened chan struct{}
	allowRefresh  chan struct{}
}

func (s *publicKeyServiceStub) GetPublicKeys(
	_ context.Context,
	_ *connect.Request[userv1.GetPublicKeysRequest],
) (*connect.Response[userv1.GetPublicKeysResponse], error) {
	s.mu.Lock()
	s.requests++
	requestNumber := s.requests
	keys := append([]*userv1.JsonWebKey(nil), s.keys...)
	err := s.err
	block := s.blockRefresh && requestNumber > 1
	opened, release := s.refreshOpened, s.allowRefresh
	s.mu.Unlock()
	if block {
		select {
		case opened <- struct{}{}:
		default:
		}
		<-release
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&userv1.GetPublicKeysResponse{Keys: keys}), nil
}

func connectKeyServer(t *testing.T, service userv1connect.PublicKeyServiceHandler) *httptest.Server {
	t.Helper()
	path, handler := userv1connect.NewPublicKeyServiceHandler(service)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// revocationCheckerStub simulates the shared revocation dictionary in tests.
type revocationCheckerStub struct {
	mu      sync.Mutex
	revoked map[string]struct{}
	err     error
}

func (s *revocationCheckerStub) IsRevoked(_ context.Context, sessionID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	_, hit := s.revoked[sessionID]
	return hit, nil
}

func (s *revocationCheckerStub) revoke(sessionIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revoked == nil {
		s.revoked = map[string]struct{}{}
	}
	for _, sessionID := range sessionIDs {
		s.revoked[sessionID] = struct{}{}
	}
}

func protoKeys(set testJWKSet) []*userv1.JsonWebKey {
	keys := make([]*userv1.JsonWebKey, 0, len(set.Keys))
	for _, key := range set.Keys {
		keys = append(keys, &userv1.JsonWebKey{
			Kty: key.KeyType, Crv: key.Curve, X: key.X, Y: key.Y,
			Use: key.Use, Alg: key.Algorithm, Kid: key.KeyID,
		})
	}
	return keys
}

func newAuthenticator(t *testing.T, baseURL string) *Authenticator {
	t.Helper()
	a, err := NewAuthenticator(context.Background(), AuthConfig{
		UserServiceURL: baseURL, Issuer: TokenIssuer, Audience: TokenAudience,
		Revocations: &revocationCheckerStub{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAuthenticatorVerifyTypedClaims(t *testing.T) {
	testAuthenticatorVerifyTypedClaims(t)
}

func testAuthenticatorVerifyTypedClaims(t *testing.T) {
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	server := connectKeyServer(t, &publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())})
	a := newAuthenticator(t, server.URL)
	now := time.Now().UTC().Truncate(time.Second)

	t.Run("standard audience array", func(t *testing.T) {
		claims := validTestClaims(now)
		claims["aud"] = []string{"another-service", TokenAudience}
		verified, err := a.verify(context.Background(), signTestToken(t, key, "key-1", claims))
		if err != nil {
			t.Fatal(err)
		}
		if verified.Subject != "user-1" || verified.SessionID != "session-1" ||
			verified.Role != "user" || verified.TokenID != "token-1" {
			t.Fatalf("unexpected claims: %+v", verified)
		}
	})

	for _, tc := range []struct {
		name   string
		mutate func(jwt.MapClaims)
	}{
		{name: "missing issued at", mutate: func(c jwt.MapClaims) { delete(c, "iat") }},
		{name: "missing expiration", mutate: func(c jwt.MapClaims) { delete(c, "exp") }},
		{name: "missing not before", mutate: func(c jwt.MapClaims) { delete(c, "nbf") }},
		{name: "wrong issuer", mutate: func(c jwt.MapClaims) { c["iss"] = "another-issuer" }},
		{name: "wrong audience", mutate: func(c jwt.MapClaims) { c["aud"] = "another-service" }},
		{name: "expired", mutate: func(c jwt.MapClaims) { c["exp"] = now.Add(-time.Second).Unix() }},
		{name: "wrong token use", mutate: func(c jwt.MapClaims) { c["token_use"] = "refresh" }},
		{name: "missing session", mutate: func(c jwt.MapClaims) { delete(c, "sid") }},
		{name: "invalid role", mutate: func(c jwt.MapClaims) { c["role"] = "owner" }},
		{name: "not before differs from issued at", mutate: func(c jwt.MapClaims) { c["nbf"] = now.Add(-time.Second).Unix() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := validTestClaims(now)
			tc.mutate(claims)
			_, err := a.verify(context.Background(), signTestToken(t, key, "key-1", claims))
			if !errors.Is(err, errInvalidAccessToken) {
				t.Fatalf("got %v, want invalid access token", err)
			}
		})
	}

	invalidTokens := map[string]string{
		"wrong signature": signTestToken(t, testSigningKey(t), "key-1", validTestClaims(now)),
		"wrong algorithm": signTestHMACToken(t, "key-1", validTestClaims(now)),
		"wrong type":      signTestTokenWithType(t, key, "key-1", "at+jwt", validTestClaims(now)),
		"missing key ID":  signTestTokenWithType(t, key, "", "JWT", validTestClaims(now)),
	}
	for name, token := range invalidTokens {
		t.Run(name, func(t *testing.T) {
			if _, err := a.verify(context.Background(), token); !errors.Is(err, errInvalidAccessToken) {
				t.Fatalf("got %v, want invalid access token", err)
			}
		})
	}
	if _, err := a.verify(context.Background(), strings.Repeat("x", 8193)); !errors.Is(err, errInvalidAccessToken) {
		t.Fatalf("oversized token: got %v, want invalid access token", err)
	}
}

func TestNewAuthenticatorRejectsInvalidJWKS(t *testing.T) {
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	valid := protoKeys(codec.PublicKeys())
	duplicate := append(append([]*userv1.JsonWebKey(nil), valid...), valid[0])
	malformed := append([]*userv1.JsonWebKey(nil), valid...)
	malformed[0].X = "not-base64url"
	wrongMetadata := append([]*userv1.JsonWebKey(nil), valid...)
	wrongMetadata[0].Alg = "RS256"
	oversized := append([]*userv1.JsonWebKey(nil), valid...)
	noisy := make([]byte, 1_300_000)
	if _, err := rand.Read(noisy); err != nil {
		t.Fatal(err)
	}
	oversized[0].X = base64.RawURLEncoding.EncodeToString(noisy)

	for _, tc := range []struct {
		name string
		keys []*userv1.JsonWebKey
	}{
		{name: "empty key set"},
		{name: "duplicate key ID", keys: duplicate},
		{name: "malformed coordinate", keys: malformed},
		{name: "wrong key metadata", keys: wrongMetadata},
		{name: "oversized response", keys: oversized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := connectKeyServer(t, &publicKeyServiceStub{keys: tc.keys})
			_, err := NewAuthenticator(context.Background(), AuthConfig{
				UserServiceURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience,
				Revocations: &revocationCheckerStub{},
			})
			if err == nil {
				t.Fatal("expected invalid key response to be rejected")
			}
		})
	}
}

func TestNewAuthenticatorAllowsHTTPServiceURL(t *testing.T) {
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	server := connectKeyServer(t, &publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())})
	_, err = NewAuthenticator(context.Background(), AuthConfig{
		UserServiceURL: server.URL,
		Issuer:         TokenIssuer, Audience: TokenAudience,
		Revocations: &revocationCheckerStub{},
	})
	if err != nil {
		t.Fatalf("HTTP service URL should be accepted: %v", err)
	}
}

func TestAuthenticatorRejectsNonOriginURLs(t *testing.T) {
	for _, rawURL := range []string{
		"http://user-service:8081/api",
		"http://user:password@user-service:8081",
		"http://user-service:8081?query=1",
		"http://user-service:8081#fragment",
		"ftp://user-service:8081",
		"://invalid",
	} {
		_, err := NewAuthenticator(context.Background(), AuthConfig{
			UserServiceURL: rawURL,
			Issuer:         TokenIssuer, Audience: TokenAudience,
		})
		if err == nil {
			t.Fatal("invalid mode or origin accepted")
		}
	}
}

func TestNewAuthenticatorDoesNotFollowRedirects(t *testing.T) {
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	service := &publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())}
	target := connectKeyServer(t, service)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	_, err = NewAuthenticator(context.Background(), AuthConfig{
		UserServiceURL: redirect.URL, Issuer: TokenIssuer, Audience: TokenAudience,
		Revocations: &revocationCheckerStub{},
	})
	if err == nil {
		t.Fatal("expected redirecting user service endpoint to be rejected")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.requests != 0 {
		t.Fatal("redirect target was contacted")
	}
}

func TestAuthenticatorKeyRotationAndUnavailableRefresh(t *testing.T) {
	firstKey := testSigningKey(t)
	secondKey := testSigningKey(t)
	firstCodec, err := newTestCodec(firstKey, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	secondCodec, err := newTestCodec(secondKey, "key-2", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	service := &publicKeyServiceStub{keys: protoKeys(firstCodec.PublicKeys())}
	server := connectKeyServer(t, service)
	a := newAuthenticator(t, server.URL)
	now := time.Now().UTC().Truncate(time.Second)
	rotated, err := secondCodec.Sign(testClaims{Type: "access", Subject: "user-1", SessionID: "session-1",
		Role: "user", IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "token-1"})
	if err != nil {
		t.Fatal(err)
	}
	service.mu.Lock()
	service.keys = protoKeys(secondCodec.PublicKeys())
	service.mu.Unlock()
	if _, err := a.verify(context.Background(), rotated); err != nil {
		t.Fatalf("rotated key was not loaded: %v", err)
	}
	service.mu.Lock()
	if service.requests != 2 {
		t.Fatalf("got %d public-key requests after rotation, want 2", service.requests)
	}
	service.mu.Unlock()

	a.refreshMu.Lock()
	a.lastMiss = time.Time{}
	a.refreshMu.Unlock()
	unknown1 := signTestToken(t, testSigningKey(t), "unknown-1", validTestClaims(now))
	unknown2 := signTestToken(t, testSigningKey(t), "unknown-2", validTestClaims(now))
	if _, err := a.verify(context.Background(), unknown1); !errors.Is(err, errInvalidAccessToken) {
		t.Fatalf("first unknown key: got %v, want invalid access token", err)
	}
	if _, err := a.verify(context.Background(), unknown2); !errors.Is(err, errInvalidAccessToken) {
		t.Fatalf("second unknown key: got %v, want invalid access token", err)
	}
	service.mu.Lock()
	if service.requests != 3 {
		t.Fatalf("got %d public-key requests after throttled misses, want 3", service.requests)
	}
	service.err = connect.NewError(connect.CodeUnavailable, errors.New("temporarily unavailable"))
	service.mu.Unlock()

	cache := a.cache.Load()
	a.cache.Store(&keyCache{keys: cache.keys, expires: time.Now().Add(-time.Second)})
	protected := a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+rotated)
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, req)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got HTTP %d, want 503", response.Code)
	}
}

func TestKnownKeyDoesNotWaitForUnknownKeyRefresh(t *testing.T) {
	knownKey := testSigningKey(t)
	unknownKey := testSigningKey(t)
	knownCodec, err := newTestCodec(knownKey, "known", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	unknownCodec, err := newTestCodec(unknownKey, "unknown", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	service := &publicKeyServiceStub{keys: protoKeys(knownCodec.PublicKeys()),
		blockRefresh: true, refreshOpened: make(chan struct{}, 1), allowRefresh: make(chan struct{})}
	server := connectKeyServer(t, service)
	a := newAuthenticator(t, server.URL)
	now := time.Now().UTC().Truncate(time.Second)
	known, err := knownCodec.Sign(testClaims{Type: "access", Subject: "user-1", SessionID: "session-1",
		Role: "user", IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "known-token"})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := unknownCodec.Sign(testClaims{Type: "access", Subject: "user-1", SessionID: "session-1",
		Role: "user", IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "unknown-token"})
	if err != nil {
		t.Fatal(err)
	}

	unknownDone := make(chan error, 1)
	go func() {
		_, err := a.verify(context.Background(), unknown)
		unknownDone <- err
	}()
	<-service.refreshOpened

	knownDone := make(chan error, 1)
	go func() {
		_, err := a.verify(context.Background(), known)
		knownDone <- err
	}()
	select {
	case err := <-knownDone:
		if err != nil {
			t.Fatalf("known key failed during refresh: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("known key verification blocked on public-key refresh")
	}

	close(service.allowRefresh)
	if err := <-unknownDone; !errors.Is(err, errInvalidAccessToken) {
		t.Fatalf("unknown key: got %v, want invalid access token", err)
	}
}

func testSigningKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestNewAuthenticatorRequiresRevocationChecker(t *testing.T) {
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	server := connectKeyServer(t, &publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())})
	_, err = NewAuthenticator(context.Background(), AuthConfig{
		UserServiceURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience,
	})
	if err == nil || !strings.Contains(err.Error(), "revocation checker is required") {
		t.Fatalf("got %v, want missing revocation checker rejection", err)
	}
}

func TestAuthenticateRejectsRevokedSession(t *testing.T) {
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	server := connectKeyServer(t, &publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())})
	checker := &revocationCheckerStub{revoked: map[string]struct{}{"revoked-session": {}}}
	a, err := NewAuthenticator(context.Background(), AuthConfig{
		UserServiceURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience, Revocations: checker,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	token := func(sessionID, tokenID string) string {
		t.Helper()
		signed, err := codec.Sign(testClaims{Type: "access", Subject: "user-1", SessionID: sessionID,
			Role: "user", IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: tokenID})
		if err != nil {
			t.Fatal(err)
		}
		return signed
	}

	for _, tc := range []struct {
		name    string
		session string
		want    int
	}{
		{name: "live session accepted", session: "session-1", want: http.StatusNoContent},
		{name: "revoked session rejected", session: "revoked-session", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			protected := a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				caller, ok := auth.CallerFromContext(r.Context())
				if !ok || caller.SessionID != tc.session {
					t.Fatal("verified caller missing")
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", "Bearer "+token(tc.session, "token-1"))
			response := httptest.NewRecorder()
			protected.ServeHTTP(response, request)
			if response.Code != tc.want || called != (tc.want == http.StatusNoContent) {
				t.Fatalf("status=%d, handler called=%v", response.Code, called)
			}
		})
	}

	// Fail closed: an unreachable revocation store rejects with 503, never
	// accepts the token.
	checker.err = errors.New("redis unavailable")
	request := httptest.NewRequest(http.MethodGet, "/protected", nil)
	request.Header.Set("Authorization", "Bearer "+token("session-1", "token-2"))
	response := httptest.NewRecorder()
	a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("request authenticated while revocation store was down")
	})).ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("got HTTP %d, want 503", response.Code)
	}
}

func validTestClaims(now time.Time) jwt.MapClaims {
	return jwt.MapClaims{
		"iss": TokenIssuer, "aud": TokenAudience, "sub": "user-1",
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Minute).Unix(),
		"jti": "token-1", "sid": "session-1", "token_use": "access", "role": "user",
	}
}

func signTestToken(t *testing.T, key *ecdsa.PrivateKey, kid string, claims jwt.MapClaims) string {
	return signTestTokenWithType(t, key, kid, "JWT", claims)
}

func signTestTokenWithType(t *testing.T, key *ecdsa.PrivateKey, kid, tokenType string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodES256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	token.Header["typ"] = tokenType
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func signTestHMACToken(t *testing.T, kid string, claims jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = kid
	signed, err := token.SignedString([]byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
