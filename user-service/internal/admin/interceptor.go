package admin

import (
	"context"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
)

// NormalizeAllowedDomainInput runs before protobuf validation so hostname rules
// apply to the same canonical input that the service persists.
func NormalizeAllowedDomainInput() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if input, ok := req.Any().(*userv1.AllowDomainRequest); ok {
				input.Domain = normalizeDomain(input.Domain)
			}
			return next(ctx, req)
		}
	})
}
