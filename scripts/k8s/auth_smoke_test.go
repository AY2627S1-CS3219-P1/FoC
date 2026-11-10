package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthSmokeLoginRefreshLogoutAndProtectedRoutes(t *testing.T) {
	requested, revoked := false, false
	profileCalls := 0
	var origin string
	user := map[string]any{"id": "user-1", "email": "admin@example.test", "role": "USER_ROLE_SUPER_ADMIN"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		write := func(value any) {
			if err := json.NewEncoder(w).Encode(value); err != nil {
				t.Error(err)
			}
		}
		cookie := func(value string, maxAge int) {
			http.SetCookie(
				w,
				&http.Cookie{
					Name:     refreshCookie,
					Value:    value,
					Path:     authPath,
					Secure:   true,
					HttpOnly: true,
					SameSite: http.SameSiteStrictMode,
					MaxAge:   maxAge,
				},
			)
		}
		unauthorized := func() { w.WriteHeader(401); write(map[string]string{"code": "unauthenticated"}) }
		switch r.URL.Path {
		case "/api/v1/messages":
			messages := []any{}
			if requested {
				messages = append(
					messages,
					map[string]any{
						"ID":      "mail-1",
						"Created": time.Now().Format(time.RFC3339Nano),
						"To":      []any{map[string]string{"Address": "admin@example.test"}},
					},
				)
			}
			write(map[string]any{"messages": messages})
		case "/api/v1/message/mail-1":
			write(
				map[string]any{
					"To":   []any{map[string]string{"Address": "admin@example.test"}},
					"Text": origin + "/login?token=one-use",
				},
			)
		case authPath + "RequestLink":
			requested = true
			write(map[string]any{})
		case authPath + "Login":
			cookie("first", 3600)
			write(map[string]any{"user": user, "accessToken": "a.b.c"})
		case profilePath:
			if r.Header.Get("Cookie") != "" {
				t.Error("refresh cookie leaked to profile")
			}
			if r.Header.Get("Authorization") == "" {
				unauthorized()
				return
			}
			profileCalls++
			write(map[string]any{"profile": user})
		case supplierPath:
			if r.Header.Get("Cookie") != "" {
				t.Error("refresh cookie leaked to Supplier")
			}
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(401)
				write(
					map[string]any{
						"data": nil,
						"status": []any{
							map[string]string{"severity": "error", "message": "invalid or missing access token"},
						},
					},
				)
				return
			}
			write(map[string]any{"page": 1, "pageSize": 1, "locations": []any{}})
		case authPath + "Refresh":
			if revoked || r.Header.Get("Cookie") == "" {
				unauthorized()
				return
			}
			cookie("second", 3600)
			write(map[string]any{"user": user, "accessToken": "d.e.f"})
		case realtimePath:
			if r.Header.Get("Authorization") == "" || r.Header.Get("Cookie") != "" {
				t.Error("invalid streaming credentials")
			}
			w.Header().Set("Content-Type", "application/connect+json")
			_, _ = w.Write([]byte{0, 0, 0, 0, 14})
			_, _ = w.Write([]byte(`{"ready":true}` + "\n"))
		case authPath + "Logout":
			revoked = true
			cookie("", -1)
			write(map[string]any{})
		default:
			if r.Header.Get("Authorization") == "" {
				t.Error("protected RPC called without JWT")
			}
			write(map[string]any{})
		}
	}))
	defer server.Close()
	origin = server.URL
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := &authProbe{
		ctx:    ctx,
		client: server.Client(),
		base:   origin,
		origin: origin,
		mail:   origin,
		email:  "admin@example.test",
	}
	if err := probe.verifySession(); err != nil {
		t.Fatal(err)
	}
	if !requested || !revoked || profileCalls != 2 || probe.cookie != "" {
		t.Fatalf("session lifecycle incomplete: requested=%v revoked=%v profiles=%d", requested, revoked, profileCalls)
	}
}

func TestAuthSmokeRejectsCookieAndRemoteEndpoints(t *testing.T) {
	for _, endpoint := range []string{"https://example.com", "https://localhost/path", "https://user:secret@localhost", "https://localhost/?token=secret"} {
		if err := loopbackOrigin(endpoint); err == nil {
			t.Errorf("accepted endpoint %q", endpoint)
		}
	}
	valid := &http.Cookie{
		Name:     refreshCookie,
		Value:    "secret",
		Path:     authPath,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
	if value, err := updateRefreshCookie("", []*http.Cookie{valid}); err != nil || value != "secret" {
		t.Fatalf("valid cookie rejected: %v", err)
	}
	for _, field := range []string{"domain", "path", "secure", "httpOnly", "sameSite"} {
		cookie := *valid
		switch field {
		case "domain":
			cookie.Domain = "localhost"
		case "path":
			cookie.Path = "/"
		case "secure":
			cookie.Secure = false
		case "httpOnly":
			cookie.HttpOnly = false
		case "sameSite":
			cookie.SameSite = http.SameSiteLaxMode
		}
		if _, err := updateRefreshCookie("", []*http.Cookie{&cookie}); err == nil {
			t.Errorf("accepted unsafe cookie field %s", field)
		}
	}
}

func TestAuthSmokeDoesNotLogTokenFromTransportErrors(t *testing.T) {
	probe := &authProbe{
		ctx:    context.Background(),
		client: &http.Client{Timeout: time.Second},
		base:   "http://127.0.0.1:1",
	}
	_, _, err := probe.request(probe.base, "/login?token=private-token", nil, nil)
	if err == nil || strings.Contains(err.Error(), "private-token") {
		t.Fatalf("transport error leaked token or request unexpectedly passed: %v", err)
	}
}
