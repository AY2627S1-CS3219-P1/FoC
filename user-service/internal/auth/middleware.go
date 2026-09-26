package auth

import (
	"log/slog"
	"net"
	"net/http"

	"user-service/internal/httpx"
)

// Middleware resolves the session cookie on every request and, if valid,
// puts the Principal in the context. It never rejects; pair with
// RequireAuth on protected routes.
func Middleware(svc *Service, cookieName string, log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			c, err := r.Cookie(cookieName)
			if err != nil || c.Value == "" {
				next.ServeHTTP(w, r)
				return
			}
			p, ok, err := svc.Authenticate(r.Context(), c.Value)
			if err != nil {
				httpx.WriteError(w, r, log, err)
				return
			}
			if ok {
				r = r.WithContext(WithPrincipal(r.Context(), p))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientMeta extracts IP (after chi's RealIP) and User-Agent.
func clientMeta(r *http.Request) ClientMeta {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if net.ParseIP(ip) == nil {
		ip = ""
	}
	return ClientMeta{IP: ip, UserAgent: r.UserAgent()}
}
