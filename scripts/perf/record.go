package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// The mode is controlled through a private file, never a public test endpoint.
// Point TELEGRAM_API_URL at this listener only in the dedicated test deployment.
func recordDependency(ctx context.Context, address, out string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(out, "notification-sends.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		_ = listener.Close()
		return err
	}
	defer func() { _ = f.Close() }()
	var mu sync.Mutex
	sequence := 0
	server := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var mode struct {
				Mode string `json:"mode"`
			}
			if err := readJSON(
				filepath.Join(out, "record-mode.json"),
				&mode,
			); err != nil &&
				!errors.Is(err, os.ErrNotExist) {
				http.Error(w, "invalid private recording mode", 500)
				return
			}
			method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			body, _ := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
			mu.Lock()
			sequence++
			id := sequence
			// No URL or bot token is written. Keep recorded fixture bodies private.
			err := json.NewEncoder(f).
				Encode(map[string]any{"sequence": id, "at": time.Now().UTC(), "method": method, "body": string(body), "mode": mode.Mode, "accepted": mode.Mode == "" || mode.Mode == "healthy" || mode.Mode == "hold_after_send"})
			mu.Unlock()
			if err != nil {
				http.Error(w, "recording unavailable", 500)
				return
			}
			switch mode.Mode {
			case "connection_failure":
				if h, ok := w.(http.Hijacker); ok {
					conn, _, err := h.Hijack()
					if err == nil {
						_ = conn.Close()
					}
					return
				}
				return
			case "server_error":
				http.Error(w, `{"ok":false,"error_code":503,"description":"controlled send failure"}`, 503)
				return
			case "timeout", "hold_after_send":
				<-r.Context().Done()
				return
			case "", "healthy":
			default:
				http.Error(w, "invalid private recording mode", 500)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			var response any = true
			switch method {
			case "getMe":
				response = map[string]any{
					"id":         3219,
					"is_bot":     true,
					"first_name": "Performance",
					"username":   "foc_perf_bot",
				}
			case "getUpdates":
				select {
				case <-r.Context().Done():
					return
				case <-time.After(20 * time.Second):
				}
				response = []any{}
			case "sendMessage":
				var message struct {
					ChatID int64  `json:"chat_id"`
					Text   string `json:"text"`
				}
				_ = json.Unmarshal(body, &message)
				response = map[string]any{
					"message_id": id,
					"date":       time.Now().Unix(),
					"chat":       map[string]any{"id": message.ChatID},
					"text":       message.Text,
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": response})
		}),
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	err = server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
