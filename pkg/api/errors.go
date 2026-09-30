package api

import (
	"net/http"

	"connectrpc.com/connect"
)

// ExternalError provides HTTP and Connect codes and a log trace for a client-facing error.
// Services can implement it with their own concrete error types.
type ExternalError interface {
	error
	// to remove after retiring the rest package
	Code() int
	GetConnectCode() connect.Code
	ErrorTrace() string
}

var MsgInternalError = "An unknown error has occurred"

type badRequestError struct {
	message string
	cause   error
}

var _ ExternalError = (*badRequestError)(nil)

func (e *badRequestError) Error() string                { return e.message }
func (e *badRequestError) Unwrap() error                { return e.cause }
func (e *badRequestError) Code() int                    { return http.StatusBadRequest }
func (e *badRequestError) GetConnectCode() connect.Code { return connect.CodeInvalidArgument }
func (e *badRequestError) ErrorTrace() string {
	if e.cause == nil {
		return e.message
	}
	return e.message + "\n" + e.cause.Error()
}
