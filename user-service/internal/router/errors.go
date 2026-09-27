package router

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
)

// TODO: standardise this into a project-level shared package
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
			slog.ErrorContext(ctx, "user-service RPC failed", "procedure", req.Spec().Procedure, "error", err)
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
	})
}
