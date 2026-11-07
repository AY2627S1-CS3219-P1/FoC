package checker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// defaultClientTimeout bounds a single HTTP check when NewHTTP is called with a
// nil client. It is deliberately generous: a benchmark measures tail latency, so
// a short timeout would turn ordinary pool-queue waits into errors and bias the
// histogram downward instead of reporting the true cost of the HTTP round trip.
const defaultClientTimeout = 30 * time.Second

// HTTP checks revocation through the benchmark's HTTP handler, paying a full
// round trip on top of the Postgres lookup.
type HTTP struct {
	client  *http.Client
	url     string
	statsFn func() *pgxpool.Stat
}

// NewHTTPClient returns an *http.Client tuned for load generation: keep-alives
// with generous connection reuse so high concurrency does not churn TCP
// connections (which would spike CPU and latency), and a long timeout so slow
// but valid requests are measured rather than dropped. A timeout <= 0 selects
// defaultClientTimeout.
func NewHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultClientTimeout
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:          2048,
			MaxIdleConnsPerHost:   2048,
			MaxConnsPerHost:       0, // unlimited: sustain any concurrency level
			IdleConnTimeout:       30 * time.Second,
			TLSHandshakeTimeout:   5 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
		Timeout: timeout,
	}
}

// NewHTTP returns an HTTP checker. When client is nil it builds the
// load-optimized client from NewHTTPClient. statsFn, when non-nil, exposes the
// in-process issuer's Postgres pool stats for the run. The returned checker does
// not own client; the caller keeps it alive for the benchmark's lifetime.
func NewHTTP(client *http.Client, url string, statsFn func() *pgxpool.Stat) *HTTP {
	if client == nil {
		client = NewHTTPClient(defaultClientTimeout)
	}
	return &HTTP{client: client, url: url, statsFn: statsFn}
}

// IsRevoked performs one GET on the /check endpoint and reads the revoked flag.
func (h *HTTP) IsRevoked(ctx context.Context, sessionID string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.url+"/check?sid="+url.QueryEscape(sessionID), nil)
	if err != nil {
		return false, fmt.Errorf("build http revocation request for session %q: %w", sessionID, err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("check http revocation for session %q: %w", sessionID, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("check http revocation for session %q: status %d", sessionID, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("read http revocation response for session %q: %w", sessionID, err)
	}

	var payload struct {
		Revoked bool `json:"revoked"`
	}

	if err := json.Unmarshal(body, &payload); err != nil {
		return false, fmt.Errorf("decode http revocation response for session %q: %w", sessionID, err)
	}

	return payload.Revoked, nil
}

// Revoke POSTs a durable revocation; the handler runs the same Postgres insert
// and returns success only after the commit, so this measures the full
// round trip through the acknowledged durable write.
func (h *HTTP) Revoke(ctx context.Context, sessionID string, ttl time.Duration) error {
	payload, err := json.Marshal(map[string]any{"sid": sessionID, "ttl_ms": ttl.Milliseconds()})
	if err != nil {
		return fmt.Errorf("encode http revoke request for session %q: %w", sessionID, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/revoke", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build http revoke request for session %q: %w", sessionID, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("revoke via http for session %q: %w", sessionID, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revoke via http for session %q: status %d", sessionID, resp.StatusCode)
	}

	return nil
}

// Delete POSTs a batch removal so the write-key population can be reset.
func (h *HTTP) Delete(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	payload, err := json.Marshal(map[string][]string{"ids": ids})
	if err != nil {
		return fmt.Errorf("encode http delete request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/delete", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build http delete request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete via http: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete via http: status %d", resp.StatusCode)
	}

	return nil
}

// PoolStats returns the issuer's Postgres pool waits for pool-wait accounting,
// or nil when the checker was built without a stats provider (Redis-equivalent:
// not applicable). Implements checker.StatProvider.
func (h *HTTP) PoolStats() *PoolStat {
	if h.statsFn == nil {
		return nil
	}
	return PoolStatFrom(h.statsFn())
}

// Close is a no-op; the shared HTTP client stays owned by the caller.
func (h *HTTP) Close() error {
	return nil
}
