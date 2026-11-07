package issuer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// statementTimeout bounds any single handler query; durable writes are far
// below it but this keeps a stuck backend from hanging the load generator.
const statementTimeout = 30 * time.Second

// Start launches an HTTP server on addr (e.g. "127.0.0.1:8089") serving the same
// Postgres lookup as the direct checker. /check answers {"revoked":bool};
// /revoke durably inserts one fresh revocation and returns 200 only after the
// commit; /delete removes a batch of session ids so the write-key population can
// be reset between measured windows. maxConns is the pgxpool size. The returned
// stop function shuts the server and pool down; statsFn exposes the pool for
// pool-wait accounting.
func Start(ctx context.Context, dsn string, maxConns int, addr string) (stop func(), statsFn func() *pgxpool.Stat, err error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, fmt.Errorf("pgxpool parse config failed: %w", err)
	}
	cfg.MaxConns = int32(maxConns)
	setStatementTimeout(cfg.ConnConfig, statementTimeout)

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("pgxpool new with config failed: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/check", func(w http.ResponseWriter, r *http.Request) {
		sid := r.URL.Query().Get("sid")
		if sid == "" {
			http.Error(w, "missing sid parameter", http.StatusBadRequest)
			return
		}

		var exists int
		err := pool.QueryRow(r.Context(), `SELECT 1 FROM revoked_access_tokens WHERE sid=$1 AND expires_at > now() LIMIT 1`, sid).Scan(&exists)
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, false)
			return
		}
		if err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, true)
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Sid   string `json:"sid"`
			TTLms int64  `json:"ttl_ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.Sid == "" {
			http.Error(w, "missing sid", http.StatusBadRequest)
			return
		}

		// Insert and return only after the durable commit, so the caller
		// measures the acknowledged durable write end to end.
		if _, err := pool.Exec(r.Context(),
			`INSERT INTO revoked_access_tokens (sid, expires_at) VALUES ($1, now() + $2) ON CONFLICT (sid) DO NOTHING`,
			body.Sid, time.Duration(body.TTLms)*time.Millisecond); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, true)
	})
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Ids []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := pool.Exec(r.Context(), `DELETE FROM revoked_access_tokens WHERE sid = ANY($1)`, body.Ids); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		writeJSON(w, true)
	})

	srv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}

	// Bind the listener before returning so callers reach an accepting socket
	// immediately (correctness checks and the measured window race no one on
	// server startup). Serve takes the already-bound listener.
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("issuer listen on %s: %w", addr, err)
	}

	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			// server stopped by shutdown
		}
	}()

	statsFn = pool.Stat

	stop = func() {
		pool.Close()
		_ = srv.Shutdown(ctx)
	}

	return stop, statsFn, nil
}

// setStatementTimeout caps any single handler query on the pool so a stuck
// backend cannot hang the load generator.
func setStatementTimeout(cfg *pgx.ConnConfig, d time.Duration) {
	if cfg == nil {
		return
	}
	params := cfg.RuntimeParams
	if params == nil {
		params = map[string]string{}
		cfg.RuntimeParams = params
	}
	params["statement_timeout"] = strconv.FormatInt(d.Milliseconds(), 10)
}

func writeJSON(w http.ResponseWriter, revoked bool) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"revoked": revoked})
}
