package auth

import (
	"net/http"
	"time"

	logic "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/auth"
)

func authCookie(name, value, path string, expiry time.Time) string {
	maxAge := int(time.Until(expiry).Seconds())
	if maxAge <= 0 {
		maxAge = -1
	}
	return (&http.Cookie{Name: name, Value: value, Path: path, Expires: expiry,
		MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}).String()
}

func clearRefreshCookie() string {
	return (&http.Cookie{Name: RefreshCookieName, Path: authServicePath, Expires: time.Unix(0, 0),
		MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode}).String()
}

func cookieValue(header http.Header, name string) string {
	request := &http.Request{Header: header}
	cookie, err := request.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func refreshCookie(tokens logic.AuthTokens) string {
	return authCookie(RefreshCookieName, tokens.RefreshToken, authServicePath, tokens.RefreshExpiry)
}
