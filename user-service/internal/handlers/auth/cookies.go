package auth

import (
	"net/http"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	logic "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
)

func refreshCookieHeader(tokens logic.AuthTokens) api.Option {
	return api.WithHeader("Set-Cookie", authCookie(RefreshCookieName, tokens.RefreshToken, "/api/auth", tokens.RefreshExpiry))
}

func authCookie(name, value, path string, expiry time.Time) string {
	maxAge := int(time.Until(expiry).Seconds())
	if maxAge <= 0 {
		maxAge = -1
	}
	return (&http.Cookie{Name: name, Value: value, Path: path, Expires: expiry,
		MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}).String()
}

func clearRefreshCookie() string {
	return (&http.Cookie{Name: RefreshCookieName, Path: "/api/auth", Expires: time.Unix(0, 0),
		MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}).String()
}

func cookieValue(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}
