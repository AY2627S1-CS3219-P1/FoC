package api

import "net/http"

// ExternalError provides the HTTP status and log trace for a client-facing error.
// Services can implement it with their own concrete error types.
type ExternalError interface {
	error
	Code() int
	ErrorTrace() string
}

var MsgInternalError = "An unknown error has occurred"

type badRequestError struct {
	message string
	cause   error
}

var _ ExternalError = (*badRequestError)(nil)

func (e *badRequestError) Error() string { return e.message }
func (e *badRequestError) Unwrap() error { return e.cause }
func (e *badRequestError) Code() int     { return http.StatusBadRequest }
func (e *badRequestError) ErrorTrace() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + "\n" + e.cause.Error()
}
