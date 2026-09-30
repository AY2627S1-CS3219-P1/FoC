package errs

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type AbortedError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*AbortedError)(nil)

func WrapAbortedError(err error, message string) *AbortedError {
	return &AbortedError{Wrapped: err, message: message}
}

func NewAbortedError(message string) *AbortedError {
	return &AbortedError{message: message}
}

func (e *AbortedError) Error() string                { return e.message }
func (e *AbortedError) Unwrap() error                { return e.Wrapped }
func (e *AbortedError) Code() int                    { return http.StatusConflict }
func (e *AbortedError) GetConnectCode() connect.Code { return connect.CodeAborted }
func (e *AbortedError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}
