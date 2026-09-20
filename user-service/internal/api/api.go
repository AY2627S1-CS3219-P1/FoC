// Package api provides HTTP handler utilities for the Reminding application.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/yihao03/reminding/exterrors"
)

var MsgInternalError = "An unknown error has occurred"

const (
	// HandlerTimeout caps handler execution. Fires 503 with the timeout envelope.
	HandlerTimeout = 15 * time.Second
	timeoutBody    = `{"status":[{"message":"request timed out","severity":"error"}],"data":null}`
)

// HTTPHandler converts the internal Handler type into a standard http.HandlerFunc.
//
//  1. Status code defaults to 200 OK for successful responses, and
//     500 Internal Server Error for unknown errors. Use ExternalError or
//     WithCode to set a specific status code and message. The 15s timeout
//     returns 503 Service Unavailable with a generic message.
//
//  2. Envelope: success bodies always use {"status":[...],"data":...} where
//     each status entry is {"message":...,"severity":...}. The 15s timeout
//     preserves that shape with 503 and
//     {"status":[{"message":"request timed out","severity":3}],"data":null}.
//     Use NewRawResponse or NewStreamResponse to bypass the envelope and write
//     raw bytes.
func HTTPHandler(env *Env, handler Handler) http.HandlerFunc {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response, err := handler(r, env)
		if err != nil {
			WriteError(err, w, r.Context())
		} else {
			writeResponse(response, w)
		}
	})

	timeout := http.TimeoutHandler(h, HandlerTimeout, timeoutBody)
	return timeout.ServeHTTP
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

// WriteError writes the error envelope. ExternalError sets code/message,
// unknown errors become 500 with a generic message.
//
// This function should only be called by middlewares only.
func WriteError(err error, w http.ResponseWriter, ctx context.Context) {
	if err == nil {
		err = errors.New("nil error passed to WriteError")
	}

	code := http.StatusInternalServerError
	msg := MsgInternalError

	var extErr exterrors.ExternalError
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
