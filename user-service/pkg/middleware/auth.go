package middleware

import (
	"context"
	"net/http"
)

type claimsKey[C any] struct{}

func ClaimsFromContext[C any](ctx context.Context) (C, bool) {
	claims, ok := ctx.Value(claimsKey[C]{}).(C)
	return claims, ok
}

type unauthorizedError struct{}

func (unauthorizedError) Error() string        { return "invalid or missing access token" }
func (e unauthorizedError) ErrorTrace() string { return e.Error() }
func (unauthorizedError) Code() int            { return http.StatusUnauthorized }
