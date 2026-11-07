package httpauth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
)

func TestLocalAuthenticationDelegatesOnlyVerifiedBearer(t *testing.T) {
	for _, tc := range []struct {
		name, header string
		want         int
	}{
		{name: "verified", header: "bearer valid-token", want: http.StatusNoContent},
		{name: "invalid", header: "Bearer invalid-token", want: http.StatusUnauthorized},
		{name: "missing", want: http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			verify := func(token string) (auth.Caller, error) {
				if token != "valid-token" {
					return auth.Caller{}, errors.New("invalid token")
				}
				return auth.Caller{ID: "user", SessionID: "session-1"}, nil
			}
			handler := AuthenticateLocal(verify, &revocationCheckerStub{})(
				http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					caller, ok := auth.CallerFromContext(r.Context())
					if !ok || caller.ID != "user" {
						t.Fatal("verified caller missing")
					}
					bearer, ok := auth.UserBearerFromContext(r.Context())
					if !ok || bearer != "Bearer valid-token" {
						t.Fatal("verified bearer missing")
					}
					w.WriteHeader(http.StatusNoContent)
				}))
			request := httptest.NewRequest(http.MethodGet, "/protected", nil)
			request.Header.Set("Authorization", tc.header)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tc.want || called != (tc.want == http.StatusNoContent) {
				t.Fatalf("status=%d, handler called=%v", response.Code, called)
			}
		})
	}
}

func TestLocalAuthenticationChecksRevocation(t *testing.T) {
	verify := func(token string) (auth.Caller, error) {
		return auth.Caller{ID: "user", SessionID: "revoked-session"}, nil
	}
	newHandler := func(checker *revocationCheckerStub) http.Handler {
		return AuthenticateLocal(verify, checker)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			t.Fatal("request reached the handler")
		}))
	}
	request := func() *http.Request {
		request := httptest.NewRequest(http.MethodGet, "/protected", nil)
		request.Header.Set("Authorization", "Bearer valid-token")
		return request
	}

	t.Run("revoked session rejected", func(t *testing.T) {
		checker := &revocationCheckerStub{}
		checker.revoke("revoked-session")
		response := httptest.NewRecorder()
		newHandler(checker).ServeHTTP(response, request())
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("got HTTP %d, want 401", response.Code)
		}
	})

	t.Run("unreachable store fails closed", func(t *testing.T) {
		checker := &revocationCheckerStub{err: errors.New("redis unavailable")}
		response := httptest.NewRecorder()
		newHandler(checker).ServeHTTP(response, request())
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("got HTTP %d, want 503", response.Code)
		}
	})
}
