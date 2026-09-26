// Package httpx has the JSON plumbing shared by every handler.
package httpx

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"user-service/internal/apperr"
)

const maxBodyBytes = 1 << 20 // 1 MiB

// HandlerFunc is an http handler that returns an error instead of writing it.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Wrap turns a HandlerFunc into an http.HandlerFunc that renders errors.
func Wrap(log *slog.Logger, fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	}
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code      string            `json:"code"`
	Message   string            `json:"message"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

// WriteError renders *apperr.Error as-is; anything else is logged and
// returned as a generic 500 so internals never leak.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	reqID := middleware.GetReqID(r.Context())
	var ae *apperr.Error
	if errors.As(err, &ae) {
		WriteJSON(w, ae.Status, errorBody{errorDetail{ae.Code, ae.Message, ae.Fields, reqID}})
		return
	}
	if log == nil {
		log = slog.Default()
	}
	log.ErrorContext(r.Context(), "request failed",
		"method", r.Method, "path", r.URL.Path, "request_id", reqID, "err", err)
	WriteJSON(w, http.StatusInternalServerError, errorBody{errorDetail{
		Code: "internal", Message: "something went wrong; please retry", RequestID: reqID,
	}})
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func NoContent(w http.ResponseWriter) { w.WriteHeader(http.StatusNoContent) }

// Decode reads a JSON body, rejecting unknown fields and bodies over 1 MiB.
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return apperr.New(http.StatusRequestEntityTooLarge, "body_too_large", "request body must be at most 1 MiB")
		}
		return apperr.New(http.StatusBadRequest, "invalid_json", "request body is not valid JSON: "+err.Error())
	}
	return nil
}

// UUIDParam parses a chi URL param as a UUID.
func UUIDParam(r *http.Request, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		return uuid.Nil, apperr.New(http.StatusBadRequest, "invalid_id", name+" must be a UUID")
	}
	return id, nil
}

// IntQuery parses an optional integer query param.
func IntQuery(r *http.Request, name string, def int) (int, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, apperr.Invalid(name, "must be an integer")
	}
	return n, nil
}
