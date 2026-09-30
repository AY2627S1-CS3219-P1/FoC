package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
)

// AccessVerifier is the token verifier consumed by the User Service itself.
// It uses the locally loaded signing key and does not fetch its own JWKS.
type AccessVerifier interface {
	Verify(string, jwt.TokenType, time.Time) (jwt.Claims, error)
}

func AuthenticateLocal(verifier AccessVerifier) func(http.Handler) http.Handler {
	errorWriter := connect.NewErrorWriter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			parts := strings.Fields(r.Header.Get("Authorization"))
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeLocalAuthError(errorWriter, w, r)
				return
			}
			claims, err := verifier.Verify(parts[1], jwt.AccessToken, time.Now())
			if err != nil {
				writeLocalAuthError(errorWriter, w, r)
				return
			}
			access := AccessClaims{Subject: claims.Subject, SessionID: claims.SessionID,
				Role: string(claims.Role), IssuedAt: claims.IssuedAt,
				ExpiresAt: claims.ExpiresAt, TokenID: claims.TokenID}
			ctx := withAccessClaims(r.Context(), access)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeLocalAuthError(writer *connect.ErrorWriter, w http.ResponseWriter, r *http.Request) {
	err := connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access token"))
	if writeErr := writer.Write(w, r, err); writeErr != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
	}
}
