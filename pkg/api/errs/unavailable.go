package errs

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type UnavailableError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*UnavailableError)(nil)

func WrapUnavailableError(err error, message string) *UnavailableError {
	return &UnavailableError{Wrapped: err, message: message}
}

func NewUnavailableError(message string) *UnavailableError {
	return &UnavailableError{message: message}
}

func (e *UnavailableError) Error() string                { return e.message }
func (e *UnavailableError) Unwrap() error                { return e.Wrapped }
func (e *UnavailableError) Code() int                    { return http.StatusServiceUnavailable }
func (e *UnavailableError) GetConnectCode() connect.Code { return connect.CodeUnavailable }
func (e *UnavailableError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}
