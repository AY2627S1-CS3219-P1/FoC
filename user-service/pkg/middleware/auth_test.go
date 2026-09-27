package middleware_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
)

func TestAccessMiddleware(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	codec, err := auth.NewES256Codec(key, "test", middleware.TokenIssuer, middleware.TokenAudience)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	access, err := codec.Sign(auth.Claims{Type: auth.AccessToken, Subject: "u1", SessionID: "s1",
		Role: auth.RoleUser, IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := codec.Sign(auth.Claims{Type: auth.RefreshToken, Subject: "u1", SessionID: "s1",
		IssuedAt: now, ExpiresAt: now.Add(time.Minute), TokenID: "j2"})
	if err != nil {
		t.Fatal(err)
	}
	keySet := codec.PublicKeys()
	path, keyHandler := userv1connect.NewPublicKeyServiceHandler(publicKeyService{keys: &userv1.GetPublicKeysResponse{
		Keys: []*userv1.JsonWebKey{{Kty: keySet.Keys[0].KeyType, Crv: keySet.Keys[0].Curve,
			X: keySet.Keys[0].X, Y: keySet.Keys[0].Y, Use: keySet.Keys[0].Use,
			Alg: keySet.Keys[0].Algorithm, Kid: keySet.Keys[0].KeyID}},
	}})
	mux := http.NewServeMux()
	mux.Handle(path, keyHandler)
	userService := httptest.NewServer(mux)
	defer userService.Close()
	t.Setenv("APP_ENV", "local")
	t.Setenv("USER_SERVICE_BASE_URL", userService.URL)
	authenticator, err := middleware.NewUserServiceAuthenticator(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	protected := authenticator.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := middleware.ClaimsFromContext[middleware.AccessClaims](r.Context())
		if !ok || claims.Subject != "u1" || claims.Role != string(auth.RoleUser) {
			t.Error("verified claims missing from context")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		name   string
		cookie string
		bearer string
		header string
		want   int
	}{
		{name: "access cookie without header", cookie: access, want: http.StatusUnauthorized},
		{name: "access bearer", bearer: access, want: http.StatusNoContent},
		{name: "access bearer with invalid cookie", bearer: access, cookie: "invalid", want: http.StatusNoContent},
		{name: "refresh bearer", bearer: refresh, want: http.StatusUnauthorized},
		{name: "invalid bearer with valid cookie", cookie: access, bearer: "invalid", want: http.StatusUnauthorized},
		{name: "missing", want: http.StatusUnauthorized},
		{name: "case insensitive scheme", header: "bearer " + access, want: http.StatusNoContent},
		{name: "wrong scheme", cookie: access, header: "Basic invalid", want: http.StatusUnauthorized},
		{name: "empty bearer", cookie: access, header: "Bearer ", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/protected", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: authhandler.RefreshCookieName, Value: tc.cookie})
			}
			if tc.bearer != "" {
				req.Header.Set("Authorization", "Bearer "+tc.bearer)
			}
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			response := httptest.NewRecorder()
			protected.ServeHTTP(response, req)
			if response.Code != tc.want {
				t.Fatalf("got %d, want %d", response.Code, tc.want)
			}
		})
	}
}

type publicKeyService struct {
	userv1connect.UnimplementedPublicKeyServiceHandler
	keys *userv1.GetPublicKeysResponse
}

func (s publicKeyService) GetPublicKeys(context.Context, *connect.Request[userv1.GetPublicKeysRequest]) (*connect.Response[userv1.GetPublicKeysResponse], error) {
	return connect.NewResponse(s.keys), nil
}
