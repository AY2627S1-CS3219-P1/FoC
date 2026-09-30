package shared

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
)

// AdminAuthorizationInterceptor runs before validation, including for invalid
// requests, so an ordinary caller cannot inspect the administrator contract.
func AdminAuthorizationInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, err := RequireCaller(ctx)
			if err != nil {
				return nil, err
			}
			if !caller.Admin {
				return nil, api.ToConnectError(ctx, errs.NewForbiddenError("permission denied"))
			}
			return next(ctx, req)
		}
	})
}
