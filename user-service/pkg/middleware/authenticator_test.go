package middleware

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
	"github.com/golang-jwt/jwt/v5"
)

func TestAuthenticatorVerifyTypedClaims(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	key := testSigningKey(t)
	codec, err := auth.NewES256Codec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(codec.PublicKeys())
	}))
	t.Cleanup(server.Close)
	a, err := NewAuthenticator(context.Background(), AuthConfig{
		JWKSURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience,
	})
	if err != nil {
		t.Fatal(err)
	}
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
	t.Setenv("APP_ENV", "local")
	key := testSigningKey(t)
	codec, err := auth.NewES256Codec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(codec.PublicKeys())
	if err != nil {
		t.Fatal(err)
	}
	var duplicate auth.JWKSet
	if err := json.Unmarshal(valid, &duplicate); err != nil {
		t.Fatal(err)
	}
	duplicate.Keys = append(duplicate.Keys, duplicate.Keys[0])
	duplicateJSON, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	var malformed auth.JWKSet
	if err := json.Unmarshal(valid, &malformed); err != nil {
		t.Fatal(err)
	}
	malformed.Keys[0].X = "not-base64url"
	malformedJSON, err := json.Marshal(malformed)
	if err != nil {
		t.Fatal(err)
	}
	var wrongMetadata auth.JWKSet
	if err := json.Unmarshal(valid, &wrongMetadata); err != nil {
		t.Fatal(err)
	}
	wrongMetadata.Keys[0].Algorithm = "RS256"
	wrongMetadataJSON, err := json.Marshal(wrongMetadata)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		body []byte
	}{
		{name: "empty set", body: []byte(`{"keys":[]}`)},
		{name: "duplicate key ID", body: duplicateJSON},
		{name: "malformed coordinate", body: malformedJSON},
		{name: "wrong key metadata", body: wrongMetadataJSON},
		{name: "trailing JSON", body: append(append([]byte{}, valid...), []byte(` {}`)...)},
		{name: "oversized response", body: []byte(strings.Repeat(" ", (1<<20)+1))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(tc.body)
			}))
			t.Cleanup(server.Close)
			if _, err := NewAuthenticator(context.Background(), AuthConfig{
				JWKSURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience,
			}); err == nil {
				t.Fatal("expected invalid JWKS to be rejected")
			}
		})
	}
}

func TestNewAuthenticatorRejectsHTTPOutsideLocalMode(t *testing.T) {
	for _, environment := range []string{"", "production"} {
		t.Run(environment, func(t *testing.T) {
			t.Setenv("APP_ENV", environment)
			_, err := NewAuthenticator(context.Background(), AuthConfig{
				JWKSURL: "http://user-service:8080/.well-known/jwks.json",
				Issuer:  TokenIssuer, Audience: TokenAudience,
			})
			if err == nil || err.Error() != "JWKS URL must use HTTPS outside local mode" {
				t.Fatalf("got %v, want insecure JWKS URL rejection", err)
			}
		})
	}
}

func TestNewAuthenticatorDoesNotFollowRedirects(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	key := testSigningKey(t)
	codec, err := auth.NewES256Codec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	targetRequests := make(chan struct{}, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		targetRequests <- struct{}{}
		_ = json.NewEncoder(w).Encode(codec.PublicKeys())
	}))
	t.Cleanup(target.Close)
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redirect.Close)

	_, err = NewAuthenticator(context.Background(), AuthConfig{
		JWKSURL: redirect.URL, Issuer: TokenIssuer, Audience: TokenAudience,
	})
	if err == nil {
		t.Fatal("expected redirecting JWKS endpoint to be rejected")
	}
	select {
	case <-targetRequests:
		t.Fatal("redirect target was contacted")
	default:
	}
}

func TestAuthenticatorKeyRotationAndUnavailableRefresh(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	firstKey := testSigningKey(t)
	secondKey := testSigningKey(t)
	firstCodec, err := auth.NewES256Codec(firstKey, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	secondCodec, err := auth.NewES256Codec(secondKey, "key-2", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}

	var state struct {
		sync.Mutex
		keys     auth.JWKSet
		status   int
		requests int
	}
	state.keys = firstCodec.PublicKeys()
	state.status = http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		state.Lock()
		defer state.Unlock()
		state.requests++
		w.WriteHeader(state.status)
		if state.status == http.StatusOK {
			_ = json.NewEncoder(w).Encode(state.keys)
		}
	}))
	t.Cleanup(server.Close)
	a, err := NewAuthenticator(context.Background(), AuthConfig{
		JWKSURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	rotated, err := secondCodec.Sign(auth.Claims{Type: auth.AccessToken, Subject: "user-1", SessionID: "session-1",
		Role: auth.RoleUser, IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "token-1"})
	if err != nil {
		t.Fatal(err)
	}
	state.Lock()
	state.keys = secondCodec.PublicKeys()
	state.Unlock()
	if _, err := a.verify(context.Background(), rotated); err != nil {
		t.Fatalf("rotated key was not loaded: %v", err)
	}
	state.Lock()
	if state.requests != 2 {
		t.Fatalf("got %d JWKS requests after rotation, want 2", state.requests)
	}
	state.Unlock()

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
	state.Lock()
	if state.requests != 3 {
		t.Fatalf("got %d JWKS requests after throttled misses, want 3", state.requests)
	}
	state.status = http.StatusServiceUnavailable
	state.Unlock()

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
	t.Setenv("APP_ENV", "local")
	knownKey := testSigningKey(t)
	unknownKey := testSigningKey(t)
	knownCodec, err := auth.NewES256Codec(knownKey, "known", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	unknownCodec, err := auth.NewES256Codec(unknownKey, "unknown", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}

	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var requestMu sync.Mutex
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestMu.Lock()
		requests++
		requestNumber := requests
		requestMu.Unlock()
		if requestNumber > 1 {
			close(refreshStarted)
			<-releaseRefresh
		}
		_ = json.NewEncoder(w).Encode(knownCodec.PublicKeys())
	}))
	t.Cleanup(server.Close)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRefresh) }) }
	t.Cleanup(release)

	a, err := NewAuthenticator(context.Background(), AuthConfig{
		JWKSURL: server.URL, Issuer: TokenIssuer, Audience: TokenAudience,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	known, err := knownCodec.Sign(auth.Claims{Type: auth.AccessToken, Subject: "user-1", SessionID: "session-1",
		Role: auth.RoleUser, IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "known-token"})
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := unknownCodec.Sign(auth.Claims{Type: auth.AccessToken, Subject: "user-1", SessionID: "session-1",
		Role: auth.RoleUser, IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "unknown-token"})
	if err != nil {
		t.Fatal(err)
	}

	unknownDone := make(chan error, 1)
	go func() {
		_, err := a.verify(context.Background(), unknown)
		unknownDone <- err
	}()
	<-refreshStarted

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
		t.Fatal("known key verification blocked on JWKS refresh")
	}

	release()
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
