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
				return auth.Caller{ID: "user"}, nil
			}
			handler := AuthenticateLocal(verify)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
