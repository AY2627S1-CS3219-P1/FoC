// Package apperr is the error type services return and handlers render.
// Each error carries its HTTP status, a stable machine code, a user-facing
// message (NFR-13: what failed + what to do next) and optional per-field
// messages.
package apperr

import (
	"net/http"
	"sort"
	"strings"
)

type Error struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func (e *Error) Error() string {
	if len(e.Fields) == 0 {
		return e.Code + ": " + e.Message
	}
	keys := make([]string, 0, len(e.Fields))
	for k := range e.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + ": " + e.Fields[k]
	}
	return e.Code + ": " + strings.Join(parts, "; ")
}

// Is matches on Code, so errors.Is(err, apperr.ErrForbidden) works even for
// copies with a different message.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// WithMessage returns a copy with a more specific message.
func (e *Error) WithMessage(msg string) *Error {
	c := *e
	c.Message = msg
	return &c
}

var (
	ErrUnauthenticated = New(http.StatusUnauthorized, "unauthenticated", "log in to continue")
	ErrForbidden       = New(http.StatusForbidden, "forbidden", "you are not allowed to do this")
	ErrNotFound        = New(http.StatusNotFound, "not_found", "not found")
	ErrValidation      = New(http.StatusUnprocessableEntity, "validation_failed", "one or more fields are invalid")
)

// Fields accumulates validation messages. Err returns nil when empty.
type Fields map[string]string

func (f Fields) Add(field, msg string) { f[field] = msg }

func (f Fields) Err() error {
	if len(f) == 0 {
		return nil
	}
	e := *ErrValidation
	e.Fields = f
	return &e
}

// Invalid is a one-field validation error.
func Invalid(field, msg string) error {
	return Fields{field: msg}.Err()
}
