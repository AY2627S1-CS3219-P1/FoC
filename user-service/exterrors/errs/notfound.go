package errs

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type NotFoundError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*NotFoundError)(nil)

func WrapNotFoundError(err error, message string) *NotFoundError {
	return &NotFoundError{
		Wrapped: err,
		message: message,
	}
}

func NewNotFoundError(message string) *NotFoundError {
	return &NotFoundError{
		message: message,
	}
}

func (e *NotFoundError) Error() string {
	return e.message
}

func (e *NotFoundError) Unwrap() error {
	return e.Wrapped
}

func (e *NotFoundError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}

func (e *NotFoundError) Code() int {
	return http.StatusNotFound
}
