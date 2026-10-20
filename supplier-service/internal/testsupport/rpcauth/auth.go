package rpcauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/golang-jwt/jwt/v5"
)

const testKeyID = "test-key"

// Auth signs access tokens the way User Service does and serves the
// matching public key, so tests exercise the real Authenticator.
// TODO: replace with a User Service test helper if one is exported.
type Auth struct {
	mu            sync.Mutex
	profiles      map[string]*userv1.Profile
	key           *ecdsa.PrivateKey
	now           func() time.Time
	Authenticator *httpauth.Authenticator
}

func New(t *testing.T) *Auth {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	path, handler := userv1connect.NewPublicKeyServiceHandler(publicKeyService{key: &key.PublicKey})
	a := &Auth{key: key, now: time.Now, profiles: make(map[string]*userv1.Profile)}
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	mux.Handle(userv1connect.NewProfileServiceHandler(&profileService{auth: a}))
	userService := httptest.NewServer(mux)
	t.Cleanup(userService.Close)

	t.Setenv("APP_ENV", "local")
	t.Setenv("USER_SERVICE_BASE_URL", userService.URL)
	authenticator, err := httpauth.NewUserServiceAuthenticator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	a.Authenticator = authenticator
	return a
}

// Token returns a signed access token for role.
func (a *Auth) Token(t *testing.T, role string) string {
	t.Helper()
	now := a.now().Truncate(time.Second)
	token := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss":       httpauth.TokenIssuer,
		"aud":       httpauth.TokenAudience,
		"sub":       "user-1",
		"sid":       "session-1",
		"jti":       "token-1",
		"token_use": "access",
		"role":      role,
		"iat":       now.Unix(),
		"nbf":       now.Unix(),
		"exp":       now.Add(time.Minute).Unix(),
	})
	token.Header["kid"] = testKeyID
	signed, err := token.SignedString(a.key)
	if err != nil {
		t.Fatal(err)
	}
	profileRole := map[string]userv1.UserRole{
		"user":           userv1.UserRole_USER_ROLE_USER,
		"admin":          userv1.UserRole_USER_ROLE_ADMIN,
		"super_admin":    userv1.UserRole_USER_ROLE_SUPER_ADMIN,
		"suspended_user": userv1.UserRole_USER_ROLE_SUSPENDED_USER,
	}[role]
	a.mu.Lock()
	a.profiles["Bearer "+signed] = &userv1.Profile{Id: "user-1", Role: profileRole}
	a.mu.Unlock()
	return signed
}

type profileService struct {
	userv1connect.UnimplementedProfileServiceHandler
	auth *Auth
}

func (s *profileService) GetMyProfile(_ context.Context, req *connect.Request[userv1.GetMyProfileRequest]) (*connect.Response[userv1.GetMyProfileResponse], error) {
	s.auth.mu.Lock()
	profile := s.auth.profiles[req.Header().Get("Authorization")]
	s.auth.mu.Unlock()
	if profile == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, nil)
	}
	return connect.NewResponse(&userv1.GetMyProfileResponse{Profile: profile}), nil
}

// Bearer signs an access token for each request from a client.
func (a *Auth) Bearer(t *testing.T, role string) connect.Option {
	return bearer(func() string { return a.Token(t, role) })
}

// Bearer adds an Authorization header to every request from a client.
func Bearer(token string) connect.Option {
	return bearer(func() string { return token })
}

func bearer(token func() string) connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Authorization", "Bearer "+token())
			return next(ctx, req)
		}
	}))
}

type publicKeyService struct {
	userv1connect.UnimplementedPublicKeyServiceHandler
	key *ecdsa.PublicKey
}

func (s publicKeyService) GetPublicKeys(
	context.Context,
	*connect.Request[userv1.GetPublicKeysRequest],
) (*connect.Response[userv1.GetPublicKeysResponse], error) {
	encode := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	point, err := s.key.Bytes()
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	x, y := point[1:33], point[33:65]
	return connect.NewResponse(&userv1.GetPublicKeysResponse{Keys: []*userv1.JsonWebKey{{
		Kty: "EC", Crv: "P-256", X: encode(x), Y: encode(y), Use: "sig", Alg: "ES256", Kid: testKeyID,
	}}}), nil
}
