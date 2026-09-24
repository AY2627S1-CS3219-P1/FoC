package errs

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

type BadRequestError struct {
	Wrapped error
	message string
}

var _ api.ExternalError = (*BadRequestError)(nil)

func WrapBadRequestError(err error, message string) *BadRequestError {
	return &BadRequestError{
		Wrapped: err,
		message: message,
	}
}

func NewBadRequestError(message string) *BadRequestError {
	return &BadRequestError{
		message: message,
	}
}

func (e *BadRequestError) Error() string {
	return e.message
}

func (e *BadRequestError) Unwrap() error {
	return e.Wrapped
}

func (e *BadRequestError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}

func (e *BadRequestError) Code() int {
	return http.StatusBadRequest
}
