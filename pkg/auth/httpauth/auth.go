package httpauth

import (
	"net/http"

	"connectrpc.com/connect"
)

type unauthorizedError struct{}

func (unauthorizedError) Error() string        { return "invalid or missing access token" }
func (e unauthorizedError) ErrorTrace() string { return e.Error() }
func (unauthorizedError) Code() int            { return http.StatusUnauthorized }
func (unauthorizedError) GetConnectCode() connect.Code {
	return connect.CodeUnauthenticated
}
