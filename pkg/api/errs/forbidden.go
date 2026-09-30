package errs

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type ForbiddenError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*ForbiddenError)(nil)

func WrapForbiddenError(err error, message string) *ForbiddenError {
	return &ForbiddenError{Wrapped: err, message: message}
}

func NewForbiddenError(message string) *ForbiddenError {
	return &ForbiddenError{message: message}
}

func (e *ForbiddenError) Error() string                { return e.message }
func (e *ForbiddenError) Unwrap() error                { return e.Wrapped }
func (e *ForbiddenError) Code() int                    { return http.StatusForbidden }
func (e *ForbiddenError) GetConnectCode() connect.Code { return connect.CodePermissionDenied }

func (e *ForbiddenError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}
