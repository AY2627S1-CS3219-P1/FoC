package auth

import (
	"net/http"
	"testing"
	"time"
)

func TestCookieLifetimeFollowsTokenExpiry(t *testing.T) {
	expiry := time.Now().Add(30 * time.Second)
	response := &http.Response{Header: http.Header{"Set-Cookie": {
		authCookie(RefreshCookieName, "token", "/api/auth", expiry),
		authCookie(RefreshCookieName, "expired", "/api/auth", time.Now().Add(-time.Second)),
	}}}
	cookies := response.Cookies()
	if cookies[0].MaxAge <= 0 || cookies[0].MaxAge > 30 || !cookies[0].Expires.Equal(expiry.Truncate(time.Second)) {
		t.Fatalf("cookie does not follow token expiry: %+v", cookies[0])
	}
	if cookies[1].MaxAge != -1 {
		t.Fatal("expired token should clear its cookie")
	}
}
