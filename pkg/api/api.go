// Package api provides HTTP handler utilities shared by FoC services.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// HandlerTimeout is the deadline for computing a handler response.
// Response bodies, including streams, are written after the handler returns.
const HandlerTimeout = 15 * time.Second

type Handler[E any] = func(*http.Request, *E) (*Response, error)

// HTTPHandler converts a service handler into a standard http.HandlerFunc.
//
//  1. Status code defaults to 200 OK for successful responses, and
//     500 Internal Server Error for unknown errors. Use an ExternalError or
//     WithCode to set a specific status code and message. A handler that has
//     not returned by the deadline gets a 503 Service Unavailable response.
//
//  2. Envelope: success bodies always use {"status":[...],"data":...} where
//     each status entry is {"message":...,"severity":...}. The timeout
//     preserves that shape with 503 and
//     {"status":[{"message":"request timed out","severity":"error"}],"data":null}.
//     Use NewRawResponse or NewStreamResponse to bypass the envelope and write
//     raw bytes.
func HTTPHandler[E any](env *E, handler Handler[E]) http.HandlerFunc {
	return httpHandler(env, handler, HandlerTimeout)
}

func httpHandler[E any](env *E, handler Handler[E], timeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		r = r.WithContext(ctx)

		type result struct {
			response *Response
			err      error
		}
		results := make(chan result, 1)
		panics := make(chan any, 1)
		go func() {
			defer func() {
				if value := recover(); value != nil {
          slog.ErrorContext(r.Context(), "handler panic", "panic", value, "stack", string(debug.Stack()))
					panics <- value
				}
			}()
			response, err := handler(r, env)
			results <- result{response, err}
		}()

		select {
		case value := <-panics:
			panic(value)
		case outcome := <-results:
			if ctx.Err() == context.DeadlineExceeded {
				writeTimeout(w)
			} else if outcome.err != nil {
				WriteError(outcome.err, w, ctx)
			} else {
				writeResponse(outcome.response, w)
			}
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				writeTimeout(w)
			}
		}
	}
}

func writeTimeout(w http.ResponseWriter) {
	writeResponse(&Response{
		Code: http.StatusServiceUnavailable,
		Status: []StatusMessage{
			{Message: "request timed out", Severity: ERROR},
		},
	}, w)
}

func writeResponse(res *Response, w http.ResponseWriter) {
	if res == nil {
		WriteError(errors.New("nil response returned from handler"), w, context.Background())
		return
	}
	for key, values := range res.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	code := res.Code
	if code == 0 {
		code = http.StatusOK
	}

	if res.Stream != nil || res.Raw != nil {
		if w.Header().Get("Content-Type") == "" {
			ct := res.ContentType
			if ct == "" {
				if res.Raw != nil {
					ct = http.DetectContentType(res.Raw)
				} else {
					ct = "application/octet-stream"
				}
			}
			w.Header().Set("Content-Type", ct)
		}
		w.WriteHeader(code)
		if res.Stream != nil {
			if _, err := io.Copy(w, res.Stream); err != nil {
				slog.Error("Error streaming response", "error", err)
			}
			return
		}
		if _, err := w.Write(res.Raw); err != nil {
			slog.Error("Error writing raw response", "error", err)
		}
		return
	}

	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}

	w.WriteHeader(code)

	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("Error encoding json", "error", err)
	}
}

// WriteError writes the error envelope. An ExternalError sets code/message;
// unknown errors become 500 with a generic message.
//
// Handlers normally call this through HTTPHandler; middleware can call it directly.
func WriteError(err error, w http.ResponseWriter, ctx context.Context) {
	if err == nil {
		err = errors.New("nil error passed to WriteError")
	}

	code := http.StatusInternalServerError
	msg := MsgInternalError

	var extErr ExternalError
	if errors.As(err, &extErr) {
		code = extErr.Code()
		msg = extErr.Error()
		slog.ErrorContext(ctx, "Error occurred", "error", extErr.ErrorTrace(), "code", code)
	} else {
		slog.ErrorContext(ctx, "Error occurred", "error", err)
	}

	w.Header().Set("Content-Type", "application/json")

	w.WriteHeader(code)
	res := Response{
		Code: code,
		Status: []StatusMessage{
			{Message: msg, Severity: ERROR},
		},
	}

	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.Error("Error encoding json", "error", err)
	}
}
