package auth

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

// RequireCaller protects an RPC even when it is mounted without HTTP authentication.
func RequireCaller() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if _, ok := CallerFromContext(ctx); !ok {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("unauthenticated"))
			}
			return next(ctx, req)
		}
	})
}

// RequireAdmin checks the caller before validation reveals the administrator contract.
func RequireAdmin() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, ok := CallerFromContext(ctx)
			if !ok {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("unauthenticated"))
			}
			if !caller.Admin {
				return nil, api.ToConnectError(ctx, errs.NewForbiddenError("permission denied"))
			}
			return next(ctx, req)
		}
	})
}
