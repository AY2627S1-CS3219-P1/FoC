package middleware

import (
	"errors"
	"net/http"

	"connectrpc.com/connect"
)

func CheckOrigin(allowedOrigin string) func(http.Handler) http.Handler {
	errorWriter := connect.NewErrorWriter()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}
			origin := r.Header.Get("Origin")
			if origin != "" && (allowedOrigin == "" || origin != allowedOrigin) {
				err := connect.NewError(connect.CodeUnauthenticated, errors.New("request origin is not allowed"))
				if writeErr := errorWriter.Write(w, r, err); writeErr != nil {
					http.Error(w, "unauthenticated", http.StatusUnauthorized)
				}
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
