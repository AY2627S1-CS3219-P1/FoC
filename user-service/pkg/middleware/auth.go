package middleware

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
)

func ClaimsFromContext[C any](ctx context.Context) (C, bool) {
	claims, ok := ctx.Value(authorization.ClaimsKey{}).(C)
	return claims, ok
}

type unauthorizedError struct{}

func (unauthorizedError) Error() string        { return "invalid or missing access token" }
func (e unauthorizedError) ErrorTrace() string { return e.Error() }
func (unauthorizedError) Code() int            { return http.StatusUnauthorized }
func (unauthorizedError) GetConnectCode() connect.Code {
	return connect.CodeUnauthenticated
}
