package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const authPath = "/user.v1.AuthService/"
const orderPath = "/order.v1.OrderService/"
const refreshCookie = "foc-refresh-token"

type session struct {
	UserID        string `json:"userId"`
	AccessToken   string `json:"accessToken"`
	RefreshCookie string `json:"refreshCookie"`
}

type challenge struct {
	UserID string `json:"userId"`
	Token  string `json:"token"`
}

type rpcClient struct {
	base    string
	http    *http.Client
	session session
}

type rpcError struct {
	Status int
	Code   string
}

func (e *rpcError) Error() string { return fmt.Sprintf("RPC HTTP %d (%s)", e.Status, e.Code) }

func newRPC(base string, s session) (*rpcClient, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("base must be an absolute public HTTPS origin")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	u.Path = authPath
	jar.SetCookies(u, []*http.Cookie{{Name: refreshCookie, Value: s.RefreshCookie, Path: authPath, Secure: true}})
	return &rpcClient{strings.TrimRight(base, "/"), &http.Client{
		Timeout: 25 * time.Second, Jar: jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}, s}, nil
}

func (c *rpcClient) call(ctx context.Context, path string, input, output any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid RPC request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	req.Header.Set("Origin", c.base)
	if c.session.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.session.AccessToken)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("RPC transport or TLS failure (credentials redacted)")
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return errors.New("RPC response receipt failed")
	}
	var envelope struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(data, &envelope)
	if resp.StatusCode != http.StatusOK || envelope.Code != "" {
		return &rpcError{resp.StatusCode, envelope.Code}
	}
	if err := json.Unmarshal(data, output); err != nil {
		return errors.New("invalid RPC JSON response")
	}
	return nil
}

func (c *rpcClient) refresh(ctx context.Context) error {
	var response struct {
		AccessToken string `json:"accessToken"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := c.call(ctx, authPath+"Refresh", struct{}{}, &response); err != nil {
		return err
	}
	if response.AccessToken == "" || response.User.ID != c.session.UserID {
		return errors.New("refresh returned invalid identity")
	}
	c.session.AccessToken = response.AccessToken
	u, _ := url.Parse(c.base + authPath)
	for _, cookie := range c.http.Jar.Cookies(u) {
		if cookie.Name == refreshCookie {
			c.session.RefreshCookie = cookie.Value
		}
	}
	return nil
}

func prepareSessions(ctx context.Context, base, path, out string) error {
	var challenges []challenge
	if err := readJSON(path, &challenges); err != nil {
		return err
	}
	if len(challenges) == 0 {
		return errors.New("magic-link challenges are required; no authentication bypass is provided")
	}
	seen := make(map[string]bool)
	for _, challenge := range challenges {
		if seen[challenge.UserID] || challenge.UserID == "" || challenge.Token == "" {
			return errors.New("challenges require unique, nonempty user IDs and tokens")
		}
		seen[challenge.UserID] = true
	}
	type loginResult struct {
		session session
		err     error
	}
	results := make(chan loginResult)
	jobs := make(chan challenge)
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for challenge := range jobs {
				s, err := loginChallenge(ctx, base, challenge)
				results <- loginResult{s, err}
			}
		}()
	}
	go func() {
		for _, challenge := range challenges {
			jobs <- challenge
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	sessions := make([]session, 0, len(challenges))
	failed := 0
	for result := range results {
		if result.err != nil {
			failed++
		} else {
			sessions = append(sessions, result.session)
		}
	}
	// Save every successful single-use login even if others failed. Tokens and
	// mailbox contents never appear in diagnostics or load-test reports.
	if err := saveJSON(filepath.Join(out, "sessions.json"), sessions); err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d logins failed; successful sessions saved privately; renew failed challenges", failed)
	}
	return nil
}

func loginChallenge(ctx context.Context, base string, challenge challenge) (session, error) {
	c, err := newRPC(base, session{})
	if err != nil {
		return session{}, err
	}
	var response struct {
		AccessToken string `json:"accessToken"`
		User        struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	if err := c.call(ctx, authPath+"Login", map[string]string{"token": challenge.Token}, &response); err != nil {
		return session{}, err
	}
	u, _ := url.Parse(c.base + authPath)
	cookie := ""
	for _, value := range c.http.Jar.Cookies(u) {
		if value.Name == refreshCookie {
			cookie = value.Value
		}
	}
	if response.User.ID != challenge.UserID || response.AccessToken == "" || cookie == "" {
		return session{}, errors.New("invalid login identity, token or cookie")
	}
	return session{challenge.UserID, response.AccessToken, cookie}, nil
}
