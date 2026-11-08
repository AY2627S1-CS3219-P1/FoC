package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const authPath = "/user.v1.AuthService/"
const profilePath = "/user.v1.ProfileService/GetMyProfile"
const supplierPath = "/supplier.location.v1.LocationDiscoveryService/ListLocations"
const refreshCookie = "foc-refresh-token"

type authProbe struct {
	ctx                               context.Context
	client                            *http.Client
	base, origin, mail, email, cookie string
}

type authOptions struct {
	token, cookie                        string
	session, unauthenticated, middleware bool
}

func loopbackOrigin(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return errors.New("smoke endpoint must be an absolute loopback HTTP(S) origin")
	}
	if !contains([]string{"http", "https"}, parsed.Scheme) ||
		!contains([]string{"localhost", "127.0.0.1", "::1"}, parsed.Hostname()) ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return errors.New("smoke endpoint must be a loopback HTTP(S) origin without credentials, path or query")
	}
	return nil
}

// Body contents, URLs and raw transport errors can contain login tokens.
// Only bounded, deliberately redacted diagnostics leave this helper.
func (a *authProbe) request(
	base, path string,
	payload any,
	headers http.Header,
) (map[string]any, *http.Response, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, nil, err
		}
		body = bytes.NewReader(data)
	}
	method := http.MethodGet
	if payload != nil {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(a.ctx, method, base+path, body)
	if err != nil {
		return nil, nil, errors.New("invalid smoke request")
	}
	req.Header = headers
	response, err := a.client.Do(req)
	if err != nil {
		return nil, nil, errors.New("request failed, timed out, redirected or failed TLS verification")
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if err != nil || len(data) > 2*1024*1024 {
		return nil, nil, errors.New("invalid or oversized smoke response")
	}
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil || result == nil {
		return nil, nil, errors.New("expected a JSON response object")
	}
	return result, response, nil
}

func updateRefreshCookie(current string, cookies []*http.Cookie) (string, error) {
	for _, cookie := range cookies {
		if cookie.Name != refreshCookie {
			continue
		}
		if cookie.Path != authPath || cookie.Domain != "" || !cookie.Secure || !cookie.HttpOnly ||
			cookie.SameSite != http.SameSiteStrictMode {
			return "", errors.New(
				"refresh cookie must be host-only, Secure, HttpOnly, SameSite=Strict and scoped to AuthService",
			)
		}
		if cookie.MaxAge < 0 || (!cookie.Expires.IsZero() && cookie.Expires.Before(time.Now())) {
			current = ""
		} else {
			current = cookie.Value
		}
	}
	return current, nil
}

func (a *authProbe) rpc(path string, body any, options authOptions) (map[string]any, error) {
	headers := http.Header{
		"Content-Type":             []string{"application/json"},
		"Connect-Protocol-Version": []string{"1"},
		"Origin":                   []string{a.origin},
	}
	if options.token != "" {
		headers.Set("Authorization", "Bearer "+options.token)
	}
	cookie := options.cookie
	if options.session {
		cookie = a.cookie
	}
	if cookie != "" {
		headers.Set("Cookie", refreshCookie+"="+cookie)
	}
	data, response, err := a.request(a.base, path, body, headers)
	if err != nil {
		return nil, err
	}
	if options.unauthenticated {
		valid := data["code"] == "unauthenticated"
		if options.middleware && data["data"] == nil {
			statuses, ok := data["status"].([]any)
			allErrors, hasMessage := ok && len(statuses) > 0, false
			for _, value := range statuses {
				status, _ := value.(map[string]any)
				allErrors = allErrors && status["severity"] == "error"
				hasMessage = hasMessage || status["message"] == "invalid or missing access token"
			}
			valid = valid || (allErrors && hasMessage)
		}
		if response.StatusCode != 401 || !valid {
			return nil, errors.New("expected HTTP 401 authentication rejection")
		}
		return data, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || data["code"] != nil {
		return nil, fmt.Errorf("RPC failed with HTTP %d", response.StatusCode)
	}
	if options.session {
		a.cookie, err = updateRefreshCookie(a.cookie, response.Cookies())
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func (a *authProbe) checkUser(value any) (map[string]any, error) {
	user, _ := value.(map[string]any)
	email, _ := user["email"].(string)
	id, _ := user["id"].(string)
	if id == "" || strings.ToLower(email) != a.email {
		return nil, errors.New("authenticated response identifies the wrong bootstrap account")
	}
	if user["role"] != "USER_ROLE_SUPER_ADMIN" && user["role"] != float64(1) {
		return nil, errors.New("bootstrap account is not a super administrator")
	}
	return user, nil
}

func accessJWT(value any) (string, error) {
	token, _ := value.(string)
	if len(strings.Split(token, ".")) != 3 {
		return "", errors.New("authentication response is missing an access JWT")
	}
	return token, nil
}

func mailRecipient(message map[string]any, email string) bool {
	recipients, _ := message["To"].([]any)
	for _, value := range recipients {
		address, _ := value.(map[string]any)
		actual, _ := address["Address"].(string)
		if strings.EqualFold(actual, email) {
			return true
		}
	}
	return false
}

func mailLoginToken(message map[string]any, origin string) (string, error) {
	text, _ := message["Text"].(string)
	markup, _ := message["HTML"].(string)
	for _, match := range regexp.MustCompile(`https?://[^\s<>"']+`).FindAllString(html.UnescapeString(text+"\n"+markup), -1) {
		link, err := url.Parse(match)
		if err != nil || link.Scheme+"://"+link.Host != origin {
			continue
		}
		if link.Path == "/register" {
			return "", errors.New("mail contains a registration link; bootstrap the account first")
		}
		if link.Path == "/login" && link.Query().Get("token") != "" {
			return link.Query().Get("token"), nil
		}
	}
	return "", errors.New("new Mailpit message has no login token for the browser origin")
}

func (a *authProbe) loginToken() (string, error) {
	inbox, response, err := a.request(a.mail, "/api/v1/messages?limit=100", nil, nil)
	if err != nil {
		return "", err
	}
	messages, ok := inbox["messages"].([]any)
	if response.StatusCode != 200 || !ok {
		return "", errors.New("Mailpit listing failed")
	}
	previous := make(map[string]bool)
	for _, value := range messages {
		message, _ := value.(map[string]any)
		id, _ := message["ID"].(string)
		previous[id] = true
	}
	if _, err := a.rpc(profilePath, map[string]any{}, authOptions{unauthenticated: true}); err != nil {
		return "", err
	}
	if _, err := a.rpc(
		supplierPath,
		map[string]any{},
		authOptions{unauthenticated: true, middleware: true},
	); err != nil {
		return "", err
	}
	requestedAt := time.Now()
	if _, err := a.rpc(authPath+"RequestLink", map[string]string{"email": a.email}, authOptions{}); err != nil {
		return "", err
	}
	for {
		inbox, response, err = a.request(a.mail, "/api/v1/messages?limit=100", nil, nil)
		if err != nil {
			return "", err
		}
		messages, ok = inbox["messages"].([]any)
		if response.StatusCode != 200 || !ok {
			return "", errors.New("Mailpit listing failed")
		}
		for _, value := range messages {
			message, _ := value.(map[string]any)
			id, _ := message["ID"].(string)
			if previous[id] || !mailRecipient(message, a.email) {
				continue
			}
			created, _ := message["Created"].(string)
			timestamp, err := time.Parse(time.RFC3339Nano, created)
			if err != nil {
				return "", errors.New("Mailpit creation timestamp is invalid")
			}
			if timestamp.Before(requestedAt.Add(-30 * time.Second)) {
				continue
			}
			if id == "" {
				return "", errors.New("Mailpit message ID missing")
			}
			detail, detailResponse, err := a.request(a.mail, "/api/v1/message/"+url.PathEscape(id), nil, nil)
			if err != nil {
				return "", err
			}
			if detailResponse.StatusCode != 200 || !mailRecipient(detail, a.email) {
				return "", errors.New("Mailpit recipient did not match")
			}
			return mailLoginToken(detail, a.origin)
		}
		if err := sleep(a.ctx, time.Second); err != nil {
			return "", errors.New("timed out waiting for Mailpit login link")
		}
	}
}

func (a *authProbe) verifySession() error {
	token, err := a.loginToken()
	if err != nil {
		return err
	}
	login, err := a.rpc(authPath+"Login", map[string]string{"token": token}, authOptions{session: true})
	if err != nil {
		return err
	}
	if a.cookie == "" {
		return errors.New("Login did not set a refresh cookie")
	}
	defer func() {
		if a.cookie != "" {
			cleanup := *a
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cleanup.ctx = ctx
			if _, err := cleanup.rpc(authPath+"Logout", map[string]any{}, authOptions{session: true}); err != nil {
				note("Auth smoke session cleanup failed (details redacted).")
			}
		}
	}()
	user, err := a.checkUser(login["user"])
	if err != nil {
		return err
	}
	access, err := accessJWT(login["accessToken"])
	if err != nil {
		return err
	}
	profile, err := a.rpc(profilePath, map[string]any{}, authOptions{token: access})
	if err != nil {
		return err
	}
	profileUser, err := a.checkUser(profile["profile"])
	if err != nil {
		return err
	}
	if profileUser["id"] != user["id"] {
		return errors.New("profile belongs to a different user")
	}
	locations, err := a.rpc(
		supplierPath,
		map[string]any{"suppliersOnly": true, "page": 1, "pageSize": 1},
		authOptions{token: access},
	)
	if err != nil {
		return err
	}
	if locations["page"] != float64(1) || locations["pageSize"] != float64(1) {
		return errors.New("Supplier list pagination differs from the contract")
	}
	if list, exists := locations["locations"]; exists {
		if _, ok := list.([]any); !ok {
			return errors.New("invalid Supplier locations response")
		}
	}
	for _, path := range []string{"/order.v1.OrderService/ListOpenOrders", "/communication.v1.TelegramLinkService/GetTelegramLink", "/user.v1.AllowedDomainAdminService/ListAllowedDomains"} {
		payload := map[string]any{}
		if strings.Contains(path, "ListOpenOrders") {
			payload = map[string]any{"page": 1, "pageSize": 1}
		}
		if _, err := a.rpc(path, payload, authOptions{token: access}); err != nil {
			return err
		}
	}
	previous := a.cookie
	if err := a.verifyRealtime(access); err != nil {
		return err
	}
	refreshed, err := a.rpc(authPath+"Refresh", map[string]any{}, authOptions{session: true})
	if err != nil {
		return err
	}
	if _, err := a.checkUser(refreshed["user"]); err != nil {
		return err
	}
	access, err = accessJWT(refreshed["accessToken"])
	if err != nil {
		return err
	}
	if a.cookie == "" || a.cookie == previous {
		return errors.New("Refresh did not rotate the cookie")
	}
	profile, err = a.rpc(profilePath, map[string]any{}, authOptions{token: access})
	if err != nil {
		return err
	}
	refreshedUser, _ := profile["profile"].(map[string]any)
	if refreshedUser["id"] != user["id"] {
		return errors.New("refreshed JWT authorized a different user")
	}
	previous = a.cookie
	if _, err := a.rpc(authPath+"Logout", map[string]any{}, authOptions{session: true}); err != nil {
		return err
	}
	if a.cookie != "" {
		return errors.New("Logout did not clear the cookie")
	}
	for _, cookie := range []string{previous, ""} {
		if _, err := a.rpc(
			authPath+"Refresh",
			map[string]any{},
			authOptions{cookie: cookie, unauthenticated: true},
		); err != nil {
			return err
		}
	}
	note(
		"PASS Mailpit magic-link login, scoped secure cookie, protected RPCs, refresh rotation, logout and revoked refresh rejection.",
	)
	return nil
}

func (o *operator) authSmoke(base, origin, mail string) error {
	for _, endpoint := range []string{base, origin, mail} {
		if err := loopbackOrigin(endpoint); err != nil {
			return err
		}
	}
	email := strings.ToLower(strings.TrimSpace(os.Getenv("K8S_BOOTSTRAP_SUPERADMIN_EMAIL")))
	if !regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`).MatchString(email) {
		return errors.New("configured bootstrap email is invalid")
	}
	if os.Getenv("NODE_TLS_REJECT_UNAUTHORIZED") == "0" {
		return errors.New("do not disable TLS verification")
	}
	milliseconds, err := strconv.Atoi(envDefault("SMOKE_TIMEOUT_MS", "90000"))
	if err != nil || milliseconds < 1000 || milliseconds > 300000 {
		return errors.New("SMOKE_TIMEOUT_MS must be between 1000 and 300000")
	}
	ca, err := os.ReadFile(envDefault("SMOKE_TLS_CA_FILE", o.localCA()))
	if err != nil {
		return err
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		roots = x509.NewCertPool()
	}
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("smoke CA has no valid certificates")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(o.ctx, time.Duration(milliseconds)*time.Millisecond)
	defer cancel()
	probe := &authProbe{
		ctx: ctx,
		client: &http.Client{
			Transport:     transport,
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") },
		},
		base:   base,
		origin: origin,
		mail:   mail,
		email:  email,
	}
	return probe.verifySession()
}
