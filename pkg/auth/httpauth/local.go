package httpauth

import (
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
)

// AuthenticateLocal uses a caller-producing verifier supplied by the issuing service.
// The issuer owns the private key; this package does not depend on its JWT codec.
func AuthenticateLocal(verify func(string) (auth.Caller, error)) func(http.Handler) http.Handler {
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
			caller, err := verify(parts[1])
			if err != nil || caller.ID == "" {
				writeLocalAuthError(errorWriter, w, r)
				return
			}
			ctx := auth.WithCaller(r.Context(), caller)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeLocalAuthError(writer *connect.ErrorWriter, w http.ResponseWriter, r *http.Request) {
	err := api.ToConnectError(r.Context(), errs.NewUnauthorizedError("invalid or missing access token"))
	if writeErr := writer.Write(w, r, err); writeErr != nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
	}
}
