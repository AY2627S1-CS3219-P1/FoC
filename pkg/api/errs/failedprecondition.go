package errs

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type FailedPreconditionError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*FailedPreconditionError)(nil)

func WrapFailedPreconditionError(err error, message string) *FailedPreconditionError {
	return &FailedPreconditionError{Wrapped: err, message: message}
}

func NewFailedPreconditionError(message string) *FailedPreconditionError {
	return &FailedPreconditionError{message: message}
}

func (e *FailedPreconditionError) Error() string                { return e.message }
func (e *FailedPreconditionError) Unwrap() error                { return e.Wrapped }
func (e *FailedPreconditionError) Code() int                    { return http.StatusPreconditionFailed }
func (e *FailedPreconditionError) GetConnectCode() connect.Code { return connect.CodeFailedPrecondition }
func (e *FailedPreconditionError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}
