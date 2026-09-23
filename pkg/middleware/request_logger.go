// Package middleware provides HTTP middleware shared by FoC services.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// RequestLogger logs when a request begins and when its response completes.
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		attrs := []any{
			"request_id", chimiddleware.GetReqID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
		}
		slog.InfoContext(r.Context(), "request started", attrs...)

		wrapped := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)
		defer func() {
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			completed := append(attrs,
				"status", status,
				"bytes", wrapped.BytesWritten(),
				"duration", time.Since(started),
			)
			switch {
			case status >= http.StatusInternalServerError:
				slog.ErrorContext(r.Context(), "request completed", completed...)
			case status >= http.StatusBadRequest:
				slog.WarnContext(r.Context(), "request completed", completed...)
			default:
				slog.InfoContext(r.Context(), "request completed", completed...)
			}
		}()

		next.ServeHTTP(wrapped, r)
	})
}
