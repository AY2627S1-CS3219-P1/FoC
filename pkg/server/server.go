package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const ShutdownTimeout = 10 * time.Second

func New(address string, handler http.Handler) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		Protocols:         protocols,
	}
}

// WithLiveness checks only this listener, without touching dependencies.
func WithLiveness(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.Handle("/", next)
	return mux
}

// Run binds every listener before serving. Any bind/serve failure shuts down
// the group and returns an error to the executable's exit path.
func Run(ctx context.Context, timeout time.Duration, servers ...*http.Server) error {
	if len(servers) == 0 {
		return errors.New("no HTTP servers configured")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	listeners := make([]net.Listener, 0, len(servers))
	for _, srv := range servers {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
		if err != nil {
			for _, bound := range listeners {
				_ = bound.Close()
			}
			return fmt.Errorf("listen %s: %w", srv.Addr, err)
		}
		listeners = append(listeners, listener)
	}
	return runBound(ctx, timeout, servers, listeners)
}

func runBound(ctx context.Context, timeout time.Duration, servers []*http.Server, listeners []net.Listener) error {
	results := make(chan error, len(servers))
	for i, srv := range servers {
		go func() {
			err := srv.Serve(listeners[i])
			results <- fmt.Errorf("serve %s: %w", listeners[i].Addr(), err)
		}()
	}

	var result error
	remaining := len(servers)
	select {
	case result = <-results:
		remaining--
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	shutdownErrors := make(chan error, len(servers))
	var shutdowns sync.WaitGroup
	for _, srv := range servers {
		shutdowns.Go(func() {
			if err := srv.Shutdown(shutdownCtx); err != nil {
				_ = srv.Close()
				shutdownErrors <- fmt.Errorf("shutdown %s: %w", srv.Addr, err)
			}
		})
	}
	shutdowns.Wait()
	close(shutdownErrors)
	for err := range shutdownErrors {
		result = errors.Join(result, err)
	}
	for range remaining {
		if err := <-results; !errors.Is(err, http.ErrServerClosed) {
			result = errors.Join(result, err)
		}
	}
	return result
}
