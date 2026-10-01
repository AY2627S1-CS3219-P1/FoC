package router

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

func normalizeRPCError() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			response, err := next(ctx, req)
			if err == nil {
				return response, nil
			}
			var connectErr *connect.Error
			if errors.As(err, &connectErr) {
				return response, err
			}
			return nil, api.ToConnectError(ctx, err)
		}
	})
}
