package errs

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type AlreadyExistsError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*AlreadyExistsError)(nil)

func WrapAlreadyExistsError(err error, message string) *AlreadyExistsError {
	return &AlreadyExistsError{Wrapped: err, message: message}
}

func NewAlreadyExistsError(message string) *AlreadyExistsError {
	return &AlreadyExistsError{message: message}
}

func (e *AlreadyExistsError) Error() string                { return e.message }
func (e *AlreadyExistsError) Unwrap() error                { return e.Wrapped }
func (e *AlreadyExistsError) Code() int                    { return http.StatusConflict }
func (e *AlreadyExistsError) GetConnectCode() connect.Code { return connect.CodeAlreadyExists }
func (e *AlreadyExistsError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}
