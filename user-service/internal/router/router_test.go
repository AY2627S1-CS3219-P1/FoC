package router_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	healthhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
	"github.com/google/uuid"
)

const frontendOrigin = "https://app.example.test"

// okPinger reports a reachable database to the health handler.
type okPinger struct{}

func (okPinger) PingContext(context.Context) error { return nil }

func testHealth() *healthhandler.Handler { return &healthhandler.Handler{DB: okPinger{}} }

type stubLogic struct {
	requestErr      error
	loginErr        error
	registerErr     error
	refreshErr      error
	logoutErr       error
	keyErr          error
	requestEmail    string
	loginToken      string
	registerToken   string
	registerProfile jwt.Profile
	refreshIn       string
	logoutIn        string
}

func (s *stubLogic) RequestLink(_ context.Context, email string) error {
	s.requestEmail = email
	return s.requestErr
}

func (s *stubLogic) Login(_ context.Context, token string) (models.User, jwt.AuthTokens, error) {
	s.loginToken = token
	return testUser(), testTokens(), s.loginErr
}

func (s *stubLogic) Register(_ context.Context, token string, profile jwt.Profile) (models.User, jwt.AuthTokens, error) {
	s.registerToken = token
	s.registerProfile = profile
	return testUser(), testTokens(), s.registerErr
}

func (s *stubLogic) Refresh(_ context.Context, token string) (models.User, jwt.AuthTokens, error) {
	s.refreshIn = token
	return testUser(), testTokens(), s.refreshErr
}

func (s *stubLogic) Logout(_ context.Context, token string) error {
	s.logoutIn = token
	return s.logoutErr
}

func (s *stubLogic) PublicKeys() (jwt.JWKSet, error) {
	return jwt.JWKSet{Keys: []jwt.JWK{{KeyType: "EC", Curve: "P-256", X: "x", Y: "y", Use: "sig", Algorithm: "ES256", KeyID: "test"}}}, s.keyErr
}

func testUser() models.User {
	return models.User{ID: uuid.MustParse("3bd7435a-201f-45d4-b858-c1081a93a63c"),
		Email: "user@example.com", DisplayName: "User", TelegramHandle: stringPtr("example_user"),
		PhoneNumber: stringPtr("+12345678"), Role: models.RoleUser}
}

func stringPtr(value string) *string { return &value }

func testTokens() jwt.AuthTokens {
	return jwt.AuthTokens{AccessToken: "access-secret", RefreshToken: "refresh-secret",
		AccessExpiry: time.Now().Add(service.AccessTokenLifetime), RefreshExpiry: time.Now().Add(service.RefreshTokenLifetime)}
}

func TestAuthConnectMethodsAndCookies(t *testing.T) {
	if authhandler.RefreshCookieName != "foc-refresh-token" {
		t.Fatal("unexpected refresh cookie name")
	}
	logic := &stubLogic{}
	handler := router.Setup(testHealth(), &authhandler.Handler{Logic: logic, AllowedOrigin: frontendOrigin})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := userv1connect.NewAuthServiceClient(server.Client(), server.URL)
	ctx := context.Background()

	requestLink := connect.NewRequest(&userv1.RequestLinkRequest{Email: "user@example.com"})
	requestLink.Header().Set("Origin", frontendOrigin)
	if _, err := client.RequestLink(ctx, requestLink); err != nil {
		t.Fatal(err)
	}
	if logic.requestEmail != "user@example.com" {
		t.Fatalf("request link email = %q", logic.requestEmail)
	}

	loginReq := connect.NewRequest(&userv1.LoginRequest{Token: "magic"})
	loginReq.Header().Set("Origin", frontendOrigin)
	login, err := client.Login(ctx, loginReq)
	if err != nil {
		t.Fatal(err)
	}
	if login.Msg.AccessToken != "access-secret" || login.Msg.User.Id != testUser().ID.String() ||
		login.Msg.User.Role != userv1.UserRole_USER_ROLE_USER || logic.loginToken != "magic" ||
		login.Msg.User.GetTelegramHandle() != "example_user" || login.Msg.User.GetPhoneNumber() != "+12345678" ||
		strings.Contains(login.Msg.String(), "refresh-secret") {
		t.Fatalf("unexpected login response: %+v", login.Msg)
	}
	loginCookies := (&http.Response{Header: login.Header()}).Cookies()
	assertRefreshCookie(t, loginCookies, "refresh-secret")
	if login.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("login response should not be cached")
	}

	registerReq := connect.NewRequest(&userv1.RegisterRequest{Token: "register-magic", DisplayName: "User",
		TelegramHandle: stringPtr("example_user"), PhoneNumber: stringPtr("+12345678")})
	registerReq.Header().Set("Origin", frontendOrigin)
	register, err := client.Register(ctx, registerReq)
	if err != nil {
		t.Fatal(err)
	}
	if register.Msg.AccessToken != "access-secret" || logic.registerToken != "register-magic" ||
		logic.registerProfile.DisplayName != "User" || logic.registerProfile.TelegramHandle == nil ||
		*logic.registerProfile.TelegramHandle != "example_user" || logic.registerProfile.PhoneNumber == nil ||
		*logic.registerProfile.PhoneNumber != "+12345678" || register.Msg.User.GetTelegramHandle() != "example_user" ||
		register.Msg.User.GetPhoneNumber() != "+12345678" {
		t.Fatalf("unexpected registration response: %+v", register.Msg)
	}
	assertRefreshCookie(t, (&http.Response{Header: register.Header()}).Cookies(), "refresh-secret")

	refreshReq := connect.NewRequest(&userv1.RefreshRequest{})
	refreshReq.Header().Set("Origin", frontendOrigin)
	refreshReq.Header().Set("Cookie", authhandler.RefreshCookieName+"=old-refresh")
	refresh, err := client.Refresh(ctx, refreshReq)
	if err != nil {
		t.Fatal(err)
	}
	if refresh.Msg.AccessToken != "access-secret" || refresh.Msg.User == nil ||
		refresh.Msg.User.DisplayName != testUser().DisplayName || logic.refreshIn != "old-refresh" {
		t.Fatalf("refresh did not use the cookie: %+v, %q", refresh.Msg, logic.refreshIn)
	}
	assertRefreshCookie(t, (&http.Response{Header: refresh.Header()}).Cookies(), "refresh-secret")
	if refresh.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("refresh response should not be cached")
	}

	logoutReq := connect.NewRequest(&userv1.LogoutRequest{})
	logoutReq.Header().Set("Origin", frontendOrigin)
	logoutReq.Header().Set("Cookie", authhandler.RefreshCookieName+"=old-refresh")
	logout, err := client.Logout(ctx, logoutReq)
	if err != nil {
		t.Fatal(err)
	}
	if logic.logoutIn != "old-refresh" || logout.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("logout did not use cookie or set cache policy: %q, %q", logic.logoutIn, logout.Header().Get("Cache-Control"))
	}
	cleared := (&http.Response{Header: logout.Header()}).Cookies()
	if len(cleared) != 1 || cleared[0].Name != authhandler.RefreshCookieName || cleared[0].MaxAge >= 0 ||
		!cleared[0].Secure || !cleared[0].HttpOnly || cleared[0].SameSite != http.SameSiteStrictMode ||
		cleared[0].Path != "/user.v1.AuthService/" {
		t.Fatalf("refresh cookie was not safely cleared: %+v", cleared)
	}
}

func TestConnectValidationOriginAndErrorCodes(t *testing.T) {
	logic := &stubLogic{}
	server := httptest.NewServer(router.Setup(testHealth(), &authhandler.Handler{Logic: logic, AllowedOrigin: frontendOrigin}))
	t.Cleanup(server.Close)
	client := userv1connect.NewAuthServiceClient(server.Client(), server.URL)
	ctx := context.Background()

	invalid := connect.NewRequest(&userv1.RequestLinkRequest{Email: "not an email"})
	if _, err := client.RequestLink(ctx, invalid); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid email returned %v", err)
	}
	if logic.requestEmail != "" {
		t.Fatal("invalid request reached auth logic")
	}

	foreign := connect.NewRequest(&userv1.RequestLinkRequest{Email: "user@example.com"})
	foreign.Header().Set("Origin", "https://foreign.example.test")
	if _, err := client.RequestLink(ctx, foreign); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("foreign origin returned %v", err)
	}
	if logic.requestEmail != "" {
		t.Fatal("foreign origin reached auth logic")
	}

	logic.requestErr = jwt.ErrUnavailable
	if _, err := client.RequestLink(ctx, connect.NewRequest(&userv1.RequestLinkRequest{Email: "user@example.com"})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("unavailable auth returned %v", err)
	}
	logic.requestErr = errors.New("database password details")
	_, err := client.RequestLink(ctx, connect.NewRequest(&userv1.RequestLinkRequest{Email: "user@example.com"}))
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "password details") {
		t.Fatalf("unexpected error was not normalized: %v", err)
	}

	logic.loginErr = jwt.ErrLoginFailed
	if _, err := client.Login(ctx, connect.NewRequest(&userv1.LoginRequest{Token: "bad"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("failed login returned %v", err)
	}
	logic.registerErr = jwt.ErrAlreadyRegistered
	if _, err := client.Register(ctx, connect.NewRequest(&userv1.RegisterRequest{Token: "magic", DisplayName: "User"})); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("duplicate registration returned %v", err)
	}
	logic.refreshErr = jwt.ErrRefreshFailed
	if _, err := client.Refresh(ctx, connect.NewRequest(&userv1.RefreshRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("failed refresh returned %v", err)
	}
}

func TestPublicKeysHealthAndRemovedRESTRoutes(t *testing.T) {
	logic := &stubLogic{}
	server := httptest.NewServer(router.Setup(testHealth(), &authhandler.Handler{Logic: logic}))
	t.Cleanup(server.Close)

	keysClient := userv1connect.NewPublicKeyServiceClient(server.Client(), server.URL)
	keys, err := keysClient.GetPublicKeys(context.Background(), connect.NewRequest(&userv1.GetPublicKeysRequest{}))
	if err != nil || len(keys.Msg.Keys) != 1 || keys.Msg.Keys[0].Kid != "test" {
		t.Fatalf("public keys response: %+v, %v", keys, err)
	}
	if keys.Header().Get("Cache-Control") != "public, max-age=300" {
		t.Fatalf("unexpected public-key cache header %q", keys.Header().Get("Cache-Control"))
	}
	healthClient := userv1connect.NewHealthServiceClient(server.Client(), server.URL)
	health, err := healthClient.Check(context.Background(), connect.NewRequest(&userv1.CheckRequest{}))
	if err != nil || health.Msg.Status != "ok" {
		t.Fatalf("health response: %+v, %v", health, err)
	}

	for _, path := range []string{"/api/auth", "/api/auth/login", "/api/auth/register", "/api/auth/refresh", "/api/auth/logout", "/api/health", "/.well-known/jwks.json"} {
		response, err := server.Client().Get(server.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Errorf("legacy route %s returned HTTP %d", path, response.StatusCode)
		}
	}
}

func TestHealthRPCRejectsOversizedRequest(t *testing.T) {
	server := httptest.NewServer(router.Setup(testHealth(), &authhandler.Handler{Logic: &stubLogic{}}))
	t.Cleanup(server.Close)

	body := bytes.NewReader(make([]byte, (1<<20)+1))
	request, err := http.NewRequest(http.MethodPost,
		server.URL+userv1connect.HealthServiceCheckProcedure, body)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/proto")
	request.Header.Set("Connect-Protocol-Version", "1")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("oversized health request returned HTTP %d, want %d", response.StatusCode, http.StatusTooManyRequests)
	}
}

func assertRefreshCookie(t *testing.T, cookies []*http.Cookie, value string) {
	t.Helper()
	if len(cookies) != 1 {
		t.Fatalf("got %d session cookies", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != authhandler.RefreshCookieName || cookie.Value != value || !cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge <= 0 || cookie.Path != "/user.v1.AuthService/" || cookie.Domain != "" {
		t.Fatalf("unsafe or unexpected refresh cookie: %+v", cookie)
	}
}
