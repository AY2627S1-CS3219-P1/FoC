package errs

import "net/http"

type UnauthorizedError struct {
	Wrapped error
	message string
}

func WrapUnauthorizedError(err error, message string) *UnauthorizedError {
	return &UnauthorizedError{
		Wrapped: err,
		message: message,
	}
}

func NewUnauthorizedError(message string) *UnauthorizedError {
	return &UnauthorizedError{
		message: message,
	}
}

func (e *UnauthorizedError) Error() string {
	return "Unauthorized: " + e.message
}

func (e *UnauthorizedError) Unwrap() error {
	return e.Wrapped
}

func (e *UnauthorizedError) ErrorTrace() string {
	if e.Wrapped == nil {
		return e.Error()
	}
	return e.Error() + "\n" + e.Wrapped.Error()
}

func (e *UnauthorizedError) Code() int {
	return http.StatusUnauthorized
}
