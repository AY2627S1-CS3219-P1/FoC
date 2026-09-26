package errs

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type ConflictError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*ConflictError)(nil)

func WrapConflictError(err error, message string) *ConflictError {
	return &ConflictError{
		Wrapped: err,
		message: message,
	}
}

func NewConflictError(message string) *ConflictError {
	return &ConflictError{
		message: message,
	}
}

func (e *ConflictError) Error() string {
	return e.message
}

func (e *ConflictError) Unwrap() error {
	return e.Wrapped
}

func (e *ConflictError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}

func (e *ConflictError) Code() int {
	return http.StatusConflict
}
