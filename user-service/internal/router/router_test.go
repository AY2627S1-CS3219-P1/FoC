package router_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
)

type stubLogic struct {
	requestErr error
	refreshIn  string
	logoutIn   string
}

func (s *stubLogic) RequestLink(context.Context, string) error {
	return s.requestErr
}

func (s *stubLogic) Login(context.Context, string) (auth.User, auth.AuthTokens, error) {
	return auth.User{ID: "u1", Email: "user@example.com", DisplayName: "User", Role: auth.RoleUser}, testTokens(), nil
}

func (s *stubLogic) Register(context.Context, string, auth.Profile) (auth.User, auth.AuthTokens, error) {
	return auth.User{ID: "u1", Email: "user@example.com", DisplayName: "User", Role: auth.RoleUser}, testTokens(), nil
}

func (s *stubLogic) Refresh(_ context.Context, token string) (auth.AuthTokens, error) {
	s.refreshIn = token
	return testTokens(), nil
}

func (s *stubLogic) Logout(_ context.Context, token string) error {
	s.logoutIn = token
	return nil
}

func (s *stubLogic) PublicKeys() (auth.JWKSet, error) {
	return auth.JWKSet{Keys: []auth.JWK{{KeyType: "EC", KeyID: "test"}}}, nil
}

func testTokens() auth.AuthTokens {
	return auth.AuthTokens{AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccessExpiry: time.Now().Add(auth.AccessTokenLifetime), RefreshExpiry: time.Now().Add(auth.RefreshTokenLifetime)}
}

func sendRequest(t *testing.T, handler http.Handler, method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range cookies {
		req.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestAuthRoutesAndCookies(t *testing.T) {
	if authhandler.RefreshCookieName != "foc-refresh-token" {
		t.Fatal("unexpected auth cookie names")
	}
	logic := &stubLogic{}
	handler := router.Setup(&deps.Env{}, &authhandler.Handler{Logic: logic})
	login := sendRequest(t, handler, http.MethodPost, "/api/auth/login", `{"token":"magic"}`)
	if login.Code != http.StatusOK || !strings.Contains(login.Body.String(), `"accessToken":"access-secret"`) || strings.Contains(login.Body.String(), "refresh-secret") {
		t.Fatalf("login response: %d %s", login.Code, login.Body.String())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d session cookies", len(cookies))
	}
	for _, cookie := range cookies {
		if !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge <= 0 {
			t.Fatalf("unsafe session cookie: %+v", cookie)
		}
	}
	if cookies[0].Name != authhandler.RefreshCookieName || cookies[0].Path != "/api/auth" || cookies[0].Domain != "" {
		t.Fatalf("unexpected cookie scope: %+v", cookies)
	}
	register := sendRequest(t, handler, http.MethodPost, "/api/auth/register", `{"token":"magic","displayName":"User"}`)
	if register.Code != http.StatusCreated || len(register.Result().Cookies()) != 1 || !strings.Contains(register.Body.String(), `"accessToken":"access-secret"`) {
		t.Fatalf("registration response: %d %s", register.Code, register.Body.String())
	}
	refreshCookie := &http.Cookie{Name: authhandler.RefreshCookieName, Value: "old-refresh"}
	refresh := sendRequest(t, handler, http.MethodPost, "/api/auth/refresh", "", refreshCookie)
	if refresh.Code != http.StatusOK || logic.refreshIn != "old-refresh" || len(refresh.Result().Cookies()) != 1 || !strings.Contains(refresh.Body.String(), `"accessToken":"access-secret"`) {
		t.Fatalf("refresh response: %d %s", refresh.Code, refresh.Body.String())
	}
	logout := sendRequest(t, handler, http.MethodPost, "/api/auth/logout", "", refreshCookie)
	if logout.Code != http.StatusOK || logic.logoutIn != "old-refresh" {
		t.Fatalf("logout response: %d %s", logout.Code, logout.Body.String())
	}
	for _, cookie := range logout.Result().Cookies() {
		if cookie.MaxAge >= 0 || !cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/api/auth" {
			t.Fatalf("cookie was not cleared safely: %+v", cookie)
		}
	}
}

func TestPublicKeysAndUnavailableAuth(t *testing.T) {
	logic := &stubLogic{requestErr: auth.ErrUnavailable}
	handler := router.Setup(&deps.Env{}, &authhandler.Handler{Logic: logic})
	keys := sendRequest(t, handler, http.MethodGet, "/.well-known/jwks.json", "")
	if keys.Code != http.StatusOK || keys.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("JWKS response: %d %s", keys.Code, keys.Body.String())
	}
	var jwks auth.JWKSet
	if err := json.Unmarshal(keys.Body.Bytes(), &jwks); err != nil || len(jwks.Keys) != 1 {
		t.Fatalf("invalid raw JWKS: %+v, %v", jwks, err)
	}
	request := sendRequest(t, handler, http.MethodPost, "/api/auth", `{"email":"user@example.com"}`)
	if request.Code != http.StatusServiceUnavailable || !strings.Contains(request.Body.String(), "authentication service unavailable") {
		t.Fatalf("unavailable auth: %d %s", request.Code, request.Body.String())
	}
	logic.requestErr = nil
	request = sendRequest(t, handler, http.MethodPost, "/api/auth", `{"email":"user@example.com"}`)
	if request.Code != http.StatusAccepted || strings.Contains(request.Body.String(), "token") {
		t.Fatalf("generic link acknowledgement: %d %s", request.Code, request.Body.String())
	}
	logic.requestErr = errors.New("unexpected failure")
	request = sendRequest(t, handler, http.MethodPost, "/api/auth", `{"email":"user@example.com"}`)
	if request.Code != http.StatusInternalServerError {
		t.Fatalf("unexpected internal error code: %d", request.Code)
	}
}

func TestAuthRejectsForeignOrigin(t *testing.T) {
	logic := &stubLogic{}
	handler := router.Setup(&deps.Env{}, &authhandler.Handler{Logic: logic, AllowedOrigin: "https://app.example.test"})
	req := httptest.NewRequest(http.MethodPost, "/api/auth/refresh", nil)
	req.Header.Set("Origin", "https://other.example.test")
	req.AddCookie(&http.Cookie{Name: authhandler.RefreshCookieName, Value: "refresh-secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusUnauthorized || logic.refreshIn != "" {
		t.Fatalf("foreign origin changed auth state: %d", response.Code)
	}
}
