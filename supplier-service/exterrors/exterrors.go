// Package exterrors is a custom error type that wraps an original error with a custmo message.
// It is used by the application to provide more context about errors that occur.
// Typically printed to logs and returned to clients by @code{api.WriteError}.
package exterrors

type ExternalError interface {
	error
	ErrorTrace() string
	Code() int
	Unwrap() error
}
