package app_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"user-service/internal/app"
	"user-service/internal/clock"
	"user-service/internal/config"
	"user-service/internal/database"
	"user-service/internal/mail"
)

// End-to-end: real router + real Postgres, in-memory mailer, fake clock.
// Runs only with TEST_DATABASE_URL set; truncates every table.

type env struct {
	t     *testing.T
	srv   *httptest.Server
	mail  *mail.Recorder
	clock *clock.Fake
}

type client struct {
	env *env
	hc  *http.Client
	id  string
}

var tokenRe = regexp.MustCompile(`token=([A-Za-z0-9_-]+)`)

func setup(t *testing.T) *env {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := database.Open(dsn, 5, 5)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`TRUNCATE users, allowed_email_domains, auth_tokens, sessions,
		role_changes, account_warnings, favourite_suppliers, admin_bootstrap CASCADE`).Error; err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, mail: &mail.Recorder{}, clock: clock.NewFake(time.Now())}
	e.srv = httptest.NewServer(app.NewRouter(app.Deps{
		DB: db,
		Config: config.Config{Auth: config.AuthConfig{
			AppBaseURL: "http://front", SessionTTL: 24 * time.Hour, CookieName: "session",
			CookieSecure: false, LinkLimitPerEmail: 5, LinkLimitPerIP: 1000,
		}},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Mailer: e.mail,
		Clock:  e.clock,
	}))
	t.Cleanup(e.srv.Close)
	return e
}

func (e *env) newClient() *client {
	jar, _ := cookiejar.New(nil)
	return &client{env: e, hc: &http.Client{Jar: jar}}
}

func (c *client) do(method, path string, body any) (int, map[string]any, *http.Response) {
	c.env.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.env.srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		c.env.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out, resp
}

func (c *client) must(want int, method, path string, body any) map[string]any {
	c.env.t.Helper()
	got, out, _ := c.do(method, path, body)
	if got != want {
		c.env.t.Fatalf("%s %s: status %d, want %d: %v", method, path, got, want, out)
	}
	return out
}

func errCode(out map[string]any) string {
	e, _ := out["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func (e *env) lastToken() string {
	e.t.Helper()
	m, ok := e.mail.Last()
	if !ok {
		e.t.Fatal("no mail sent")
	}
	sub := tokenRe.FindStringSubmatch(m.Body)
	if sub == nil {
		e.t.Fatalf("no token in mail: %q", m.Body)
	}
	return sub[1]
}

// register runs the full magic-link registration and leaves c logged in.
func (e *env) register(email, name string) *client {
	e.t.Helper()
	c := e.newClient()
	c.must(202, "POST", "/auth/register", map[string]any{"email": email})
	out := c.must(201, "POST", "/auth/register/verify", map[string]any{
		"token": e.lastToken(), "display_name": name, "telegram_handle": "@" + strings.ReplaceAll(name, " ", "_") + "_tg",
	})
	c.id = out["id"].(string)
	return c
}

func (e *env) login(email string) *client {
	e.t.Helper()
	c := e.newClient()
	c.must(202, "POST", "/auth/login", map[string]any{"email": email})
	out := c.must(200, "POST", "/auth/login/verify", map[string]any{"token": e.lastToken()})
	c.id = out["id"].(string)
	return c
}

func TestEndToEnd(t *testing.T) {
	e := setup(t)

	// ---------------- registration ----------------
	super := e.register("root@u.nus.edu", "Root")
	alice := e.register("alice@u.nus.edu", "Alice")
	bob := e.register("bob@u.nus.edu", "Bob")

	t.Run("first signup is super_admin, later ones are users", func(t *testing.T) {
		if r := super.must(200, "GET", "/users/me", nil)["role"]; r != "super_admin" {
			t.Fatalf("root role = %v", r)
		}
		if r := alice.must(200, "GET", "/users/me", nil)["role"]; r != "user" {
			t.Fatalf("alice role = %v", r)
		}
		hist := super.must(200, "GET", "/users/"+super.id+"/role-changes", nil)["data"].([]any)
		if len(hist) != 1 {
			t.Fatalf("bootstrap history = %v", hist)
		}
	})

	t.Run("session cookie attributes", func(t *testing.T) {
		c := e.newClient()
		c.must(202, "POST", "/auth/login", map[string]any{"email": "alice@u.nus.edu"})
		_, _, resp := c.do("POST", "/auth/login/verify", map[string]any{"token": e.lastToken()})
		ck := resp.Cookies()[0]
		if !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != int((24*time.Hour).Seconds()) {
			t.Fatalf("cookie = %+v", ck)
		}
	})

	t.Run("register validation and duplicates", func(t *testing.T) {
		c := e.newClient()
		if _, out, _ := c.do("POST", "/auth/register", map[string]any{"email": "nope"}); errCode(out) != "validation_failed" {
			t.Fatalf("bad email: %v", out)
		}
		st, out, _ := c.do("POST", "/auth/register", map[string]any{"email": "ALICE@u.nus.edu"})
		if st != 409 || errCode(out) != "email_registered" {
			t.Fatalf("dup email: %d %v", st, out)
		}
	})

	t.Run("registration link: validation doesn't burn it, then single use", func(t *testing.T) {
		c := e.newClient()
		c.must(202, "POST", "/auth/register", map[string]any{"email": "carol@u.nus.edu"})
		tok := e.lastToken()
		out := c.must(422, "POST", "/auth/register/verify", map[string]any{"token": tok, "display_name": "Carol"})
		if _, ok := out["error"].(map[string]any)["fields"].(map[string]any)["telegram_handle"]; !ok {
			t.Fatalf("expected contact-method error: %v", out)
		}
		c.must(201, "POST", "/auth/register/verify", map[string]any{"token": tok, "display_name": "Carol", "phone_number": "91234567"})
		out = e.newClient().must(400, "POST", "/auth/register/verify", map[string]any{"token": tok, "display_name": "Carol2", "phone_number": "91234567"})
		if errCode(out) != "registration_link_invalid" {
			t.Fatalf("reuse: %v", out)
		}
	})

	t.Run("registration link expires after 10 minutes", func(t *testing.T) {
		c := e.newClient()
		c.must(202, "POST", "/auth/register", map[string]any{"email": "dave@u.nus.edu"})
		tok := e.lastToken()
		e.clock.Advance(10*time.Minute + time.Second)
		c.must(400, "POST", "/auth/register/verify", map[string]any{"token": tok, "display_name": "Dave", "phone_number": "91234567"})
		c.must(400, "POST", "/auth/register/verify", map[string]any{"token": "garbage", "display_name": "Dave", "phone_number": "91234567"})
	})

	t.Run("rate limit per email", func(t *testing.T) {
		c := e.newClient()
		for i := 0; i < 5; i++ {
			c.must(202, "POST", "/auth/register", map[string]any{"email": "spam@u.nus.edu"})
		}
		if st, out, _ := c.do("POST", "/auth/register", map[string]any{"email": "spam@u.nus.edu"}); st != 429 {
			t.Fatalf("6th link: %d %v", st, out)
		}
	})

	// ---------------- login / logout ----------------
	t.Run("login links: unknown email silent, older links stay valid, single use, expiry", func(t *testing.T) {
		before := len(e.mail.Sent)
		e.newClient().must(202, "POST", "/auth/login", map[string]any{"email": "ghost@u.nus.edu"})
		if len(e.mail.Sent) != before {
			t.Fatal("mail sent for unknown account")
		}

		c := e.newClient()
		c.must(202, "POST", "/auth/login", map[string]any{"email": "bob@u.nus.edu"})
		first := e.lastToken()
		c.must(202, "POST", "/auth/login", map[string]any{"email": "bob@u.nus.edu"})
		second := e.lastToken()
		c.must(200, "POST", "/auth/login/verify", map[string]any{"token": first})
		out := c.must(400, "POST", "/auth/login/verify", map[string]any{"token": first})
		if errCode(out) != "login_link_invalid" {
			t.Fatalf("reuse: %v", out)
		}
		c.must(200, "POST", "/auth/login/verify", map[string]any{"token": second})

		c.must(202, "POST", "/auth/login", map[string]any{"email": "bob@u.nus.edu"})
		third := e.lastToken()
		e.clock.Advance(11 * time.Minute)
		c.must(400, "POST", "/auth/login/verify", map[string]any{"token": third})
	})

	t.Run("logout and logout-all", func(t *testing.T) {
		e.newClient().must(401, "GET", "/users/me", nil)

		a1 := e.login("bob@u.nus.edu")
		a1.must(204, "POST", "/auth/logout", nil)
		a1.must(401, "GET", "/users/me", nil)

		b1 := e.login("bob@u.nus.edu")
		b2 := e.login("bob@u.nus.edu")
		b1.must(204, "POST", "/auth/logout-all", nil)
		b2.must(401, "GET", "/users/me", nil)
		bob.must(401, "GET", "/users/me", nil) // original session too
		bob = e.login("bob@u.nus.edu")
	})

	t.Run("sessions expire", func(t *testing.T) {
		c := e.login("alice@u.nus.edu")
		e.clock.Advance(25 * time.Hour)
		c.must(401, "GET", "/users/me", nil)
		// re-login everyone we still use
		super, alice, bob = e.login("root@u.nus.edu"), e.login("alice@u.nus.edu"), e.login("bob@u.nus.edu")
	})

	// ---------------- domains ----------------
	t.Run("domain whitelist", func(t *testing.T) {
		alice.must(403, "GET", "/admin/email-domains", nil)
		d := super.must(201, "POST", "/admin/email-domains", map[string]any{"domain": "U.NUS.edu"})
		super.must(409, "POST", "/admin/email-domains", map[string]any{"domain": "u.nus.edu"})
		super.must(422, "POST", "/admin/email-domains", map[string]any{"domain": "https://x"})
		c := e.newClient()
		out := c.must(422, "POST", "/auth/register", map[string]any{"email": "eve@gmail.com"})
		if _, ok := out["error"].(map[string]any)["fields"].(map[string]any)["email"]; !ok {
			t.Fatalf("expected email field error: %v", out)
		}
		c.must(202, "POST", "/auth/register", map[string]any{"email": "eve@u.nus.edu"})
		if n := len(super.must(200, "GET", "/admin/email-domains", nil)["data"].([]any)); n != 1 {
			t.Fatalf("domains = %d", n)
		}
		super.must(204, "DELETE", "/admin/email-domains/"+d["id"].(string), nil)
		super.must(404, "DELETE", "/admin/email-domains/"+d["id"].(string), nil)
	})

	// ---------------- roles ----------------
	var admin *client
	t.Run("role changes follow the hierarchy", func(t *testing.T) {
		// only super can promote to admin
		alice.must(403, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "admin"})
		super.must(200, "PUT", "/users/"+alice.id+"/role", map[string]any{"role": "admin"})
		admin = alice

		// admin: user <-> suspended, reason required
		out := admin.must(422, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "suspended"})
		if _, ok := out["error"].(map[string]any)["fields"].(map[string]any)["reason"]; !ok {
			t.Fatalf("expected reason error: %v", out)
		}
		admin.must(200, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "suspended", "reason": "spam"})
		if r := bob.must(200, "GET", "/users/me", nil)["role"]; r != "suspended" {
			t.Fatalf("suspension not immediate: %v", r)
		}
		admin.must(422, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "suspended", "reason": "again"})
		admin.must(403, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "admin"})
		admin.must(200, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "user", "reason": "appeal upheld"})

		// admin can't touch admins / super / self; super can't touch self
		admin.must(403, "PUT", "/users/"+super.id+"/role", map[string]any{"role": "user"})
		admin.must(403, "PUT", "/users/"+admin.id+"/role", map[string]any{"role": "user"})
		super.must(403, "PUT", "/users/"+super.id+"/role", map[string]any{"role": "admin"})
		super.must(403, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "super_admin"})
		super.must(403, "PUT", "/users/"+bob.id+"/role", map[string]any{"role": "root"})

		hist := bob.must(200, "GET", "/users/"+bob.id+"/role-changes", nil)["data"].([]any)
		if len(hist) != 2 {
			t.Fatalf("history = %v", hist)
		}
		alice2 := e.login("carol@u.nus.edu")
		alice2.must(403, "GET", "/users/"+bob.id+"/role-changes", nil)
	})

	// ---------------- profiles ----------------
	t.Run("profile visibility and edits", func(t *testing.T) {
		pub := bob.must(200, "GET", "/users/"+super.id, nil)
		if _, leaked := pub["email"]; leaked {
			t.Fatalf("public profile leaks email: %v", pub)
		}
		full := admin.must(200, "GET", "/users/"+bob.id, nil)
		if full["email"] != "bob@u.nus.edu" {
			t.Fatalf("admin view: %v", full)
		}
		bob.must(403, "GET", "/users", nil)
		list := admin.must(200, "GET", "/users?role=user&limit=2", nil)
		if list["limit"].(float64) != 2 {
			t.Fatalf("list: %v", list)
		}
		admin.must(422, "GET", "/users?role=root", nil)

		bob.must(200, "PATCH", "/users/me", map[string]any{"description": "hi"})
		bob.must(422, "PATCH", "/users/me", map[string]any{"telegram_handle": ""}) // last contact
		bob.must(403, "PATCH", "/users/"+admin.id, map[string]any{"description": "x"})
		admin.must(200, "PATCH", "/users/"+bob.id, map[string]any{"display_name": "Bobby"})
		admin.must(403, "PATCH", "/users/"+super.id, map[string]any{"description": "x"})
	})

	// ---------------- warnings ----------------
	t.Run("warnings", func(t *testing.T) {
		req := uuid.NewString()
		ev := uuid.NewString()
		body := map[string]any{"request_id": req, "reason": "cancelled after pickup", "source_event_id": ev}
		bob.must(403, "POST", "/users/"+admin.id+"/warnings", body)
		admin.must(422, "POST", "/users/"+bob.id+"/warnings", map[string]any{"request_id": req})
		w1 := admin.must(201, "POST", "/users/"+bob.id+"/warnings", body)
		w2 := admin.must(200, "POST", "/users/"+bob.id+"/warnings", body) // replay
		if w1["id"] != w2["id"] {
			t.Fatal("replayed event created a second warning")
		}
		mine := bob.must(200, "GET", "/users/me/warnings", nil)["data"].([]any)
		if len(mine) != 1 || mine[0].(map[string]any)["request_id"] != req {
			t.Fatalf("my warnings: %v", mine)
		}
		bob.must(403, "POST", "/warnings/"+w1["id"].(string)+"/remove", map[string]any{"reason": "x"})
		admin.must(422, "POST", "/warnings/"+w1["id"].(string)+"/remove", map[string]any{})
		rm := admin.must(200, "POST", "/warnings/"+w1["id"].(string)+"/remove", map[string]any{"reason": "appeal overturned", "appeal_id": uuid.NewString()})
		if rm["status"] != "removed" {
			t.Fatalf("remove: %v", rm)
		}
		admin.must(409, "POST", "/warnings/"+w1["id"].(string)+"/remove", map[string]any{"reason": "again"})
		admin.must(404, "POST", "/warnings/"+uuid.NewString()+"/remove", map[string]any{"reason": "x"})
	})

	// ---------------- favourites ----------------
	t.Run("favourites are idempotent", func(t *testing.T) {
		sup := uuid.NewString()
		bob.must(204, "PUT", "/users/me/favourites/"+sup, nil)
		bob.must(204, "PUT", "/users/me/favourites/"+sup, nil)
		if n := len(bob.must(200, "GET", "/users/me/favourites", nil)["data"].([]any)); n != 1 {
			t.Fatalf("favourites = %d", n)
		}
		bob.must(204, "DELETE", "/users/me/favourites/"+sup, nil)
		bob.must(204, "DELETE", "/users/me/favourites/"+sup, nil)
		if n := len(bob.must(200, "GET", "/users/me/favourites", nil)["data"].([]any)); n != 0 {
			t.Fatalf("favourites after delete = %d", n)
		}
		bob.must(400, "PUT", "/users/me/favourites/not-a-uuid", nil)
	})

	// ---------------- delete ----------------
	t.Run("delete user", func(t *testing.T) {
		bob.must(403, "DELETE", "/users/"+admin.id, nil)
		admin.must(403, "DELETE", "/users/"+admin.id, nil)
		admin.must(204, "DELETE", "/users/"+bob.id, nil)
		bob.must(401, "GET", "/users/me", nil) // sessions cascade
		admin.must(404, "DELETE", "/users/"+bob.id, nil)
	})
}
