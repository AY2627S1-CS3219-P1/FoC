package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/exterrors/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/api"
)

const (
	ErrRetrieveFirebaseClient = "Error retrieving firebase client"
	ErrUnauthorized           = "Unauthorized access"
	ErrInvalidToken           = "Invalid Firebase ID token"
)

func GetAuthMiddleware(env *api.Env) func(http.Handler) http.Handler {
	client, err := env.Firebase.Auth(context.Background())
	if err != nil {
		slog.Error("Error getting firebase auth client", "error", err)
		panic(err)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				api.WriteError(errs.NewUnauthorizedError(ErrUnauthorized+": Missing Authorization token"), w, r.Context())
				return
			}

			tokenString := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenString == authHeader { // Prefix wasn't found
				api.WriteError(errs.NewUnauthorizedError(ErrUnauthorized), w, r.Context())
				return
			}

			token, err := client.VerifyIDTokenAndCheckRevoked(r.Context(), tokenString)
			if err != nil {
				api.WriteError(errs.WrapUnauthorizedError(err, ErrInvalidToken), w, r.Context())
				return
			}

			// Add the UID to the context so handlers can access it
			ctx := context.WithValue(r.Context(), UserUIDKey, token.UID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func GetUserUIDFromContext(ctx context.Context) (string, bool) {
	val := ctx.Value(UserUIDKey)
	if val == nil {
		return "", false
	} else {
		return val.(string), true
	}
}
