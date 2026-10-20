package httpauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
)

type profileServiceFunc func(context.Context, *connect.Request[userv1.GetMyProfileRequest]) (*connect.Response[userv1.GetMyProfileResponse], error)

func (f profileServiceFunc) GetMyProfile(ctx context.Context, req *connect.Request[userv1.GetMyProfileRequest]) (*connect.Response[userv1.GetMyProfileResponse], error) {
	return f(ctx, req)
}

func (f profileServiceFunc) UpdateMyProfile(context.Context, *connect.Request[userv1.UpdateMyProfileRequest]) (*connect.Response[userv1.UpdateMyProfileResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("unused"))
}

func TestCurrentRoleOverridesExistingSession(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	keys := &publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())}
	profile := &userv1.Profile{Id: "user-1", Role: userv1.UserRole_USER_ROLE_USER}
	var lookupErr error
	lookups := 0
	mux := http.NewServeMux()
	mux.Handle(userv1connect.NewPublicKeyServiceHandler(keys))
	mux.Handle(userv1connect.NewProfileServiceHandler(profileServiceFunc(func(_ context.Context, req *connect.Request[userv1.GetMyProfileRequest]) (*connect.Response[userv1.GetMyProfileResponse], error) {
		lookups++
		if req.Header().Get("Authorization") == "" || req.Header().Get("Cookie") != "" {
			t.Error("lookup must forward only bearer credentials")
		}
		if lookupErr != nil {
			return nil, lookupErr
		}
		return connect.NewResponse(&userv1.GetMyProfileResponse{Profile: profile}), nil
	})))
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	a := newAuthenticator(t, server.URL)
	now := time.Now().UTC().Truncate(time.Second)
	claims := validTestClaims(now)
	claims["role"] = "admin"
	token := signTestToken(t, key, "key-1", claims)
	var caller auth.Caller
	called := false
	protected := a.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		caller, _ = auth.CallerFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func(bearer string) int {
		called = false
		r := httptest.NewRequest(http.MethodPost, "/protected", nil)
		r.Header.Set("Authorization", "Bearer "+bearer)
		r.AddCookie(&http.Cookie{Name: "refresh_token", Value: "must-not-forward"})
		w := httptest.NewRecorder()
		protected.ServeHTTP(w, r)
		return w.Code
	}

	for _, tc := range []struct {
		role  userv1.UserRole
		want  string
		admin bool
	}{
		{userv1.UserRole_USER_ROLE_USER, "user", false},
		{userv1.UserRole_USER_ROLE_SUSPENDED_USER, "suspended_user", false},
		{userv1.UserRole_USER_ROLE_USER, "user", false},
		{userv1.UserRole_USER_ROLE_ADMIN, "admin", true},
		{userv1.UserRole_USER_ROLE_SUPER_ADMIN, "super_admin", true},
	} {
		profile.Role = tc.role
		if code := request(token); code != http.StatusNoContent || !called || caller.Role != tc.want || caller.Admin != tc.admin {
			t.Fatalf("current role %v: HTTP %d, caller %+v", tc.role, code, caller)
		}
		if caller.ID != "user-1" || caller.SessionID != "session-1" || caller.TokenID != "token-1" || !caller.IssuedAt.Equal(now) {
			t.Fatalf("role lookup changed verified identity: %+v", caller)
		}
	}
	if lookups != 5 {
		t.Fatalf("role changes must be visible on every request; got %d lookups", lookups)
	}
	for _, tc := range []struct {
		name    string
		profile *userv1.Profile
		err     error
		want    int
	}{
		{"unavailable", profile, connect.NewError(connect.CodeUnavailable, errors.New("offline")), 503},
		{"permission denied does not fall back to JWT", profile, connect.NewError(connect.CodePermissionDenied, errors.New("denied")), 503},
		{"missing user", profile, connect.NewError(connect.CodeNotFound, errors.New("deleted")), 401},
		{"unauthenticated", profile, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid")), 401},
		{"missing profile", nil, nil, 503},
		{"wrong subject", &userv1.Profile{Id: "other", Role: userv1.UserRole_USER_ROLE_ADMIN}, nil, 503},
		{"unknown role", &userv1.Profile{Id: "user-1", Role: userv1.UserRole(99)}, nil, 503},
		{"unspecified role", &userv1.Profile{Id: "user-1"}, nil, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile, lookupErr = tc.profile, tc.err
			if code := request(token); code != tc.want || called {
				t.Fatalf("HTTP %d, handler called %v; want HTTP %d and no handler", code, called, tc.want)
			}
		})
	}
	before := lookups
	if code := request("invalid"); code != 401 || called || lookups != before {
		t.Fatalf("invalid identity reached role lookup: HTTP %d, lookups %d", code, lookups)
	}
}

func TestCurrentRoleDoesNotFollowRedirects(t *testing.T) {
	t.Setenv("APP_ENV", "local")
	key := testSigningKey(t)
	codec, err := newTestCodec(key, "key-1", TokenIssuer, TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	redirected := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected = true }))
	t.Cleanup(target.Close)
	mux := http.NewServeMux()
	mux.Handle(userv1connect.NewPublicKeyServiceHandler(&publicKeyServiceStub{keys: protoKeys(codec.PublicKeys())}))
	mux.HandleFunc(userv1connect.ProfileServiceGetMyProfileProcedure, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	a := newAuthenticator(t, server.URL)
	if _, err := a.currentRole(context.Background(), "token", "user-1"); !errors.Is(err, errRoleUnavailable) || redirected {
		t.Fatalf("role redirect: error %v, target contacted %v", err, redirected)
	}
}
