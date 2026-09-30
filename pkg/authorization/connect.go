package authorization

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
)

// NewConnectInterceptor enforces the policy assigned to each protected unary
// RPC using the principal attached by authentication middleware.
func NewConnectInterceptor(policies map[string]Policy) connect.UnaryInterceptorFunc {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			policy, ok := policies[req.Spec().Procedure]
			if !ok {
				return nil, api.ToConnectError(ctx, errors.New("protected RPC authorization is not configured"))
			}
			principal, err := PrincipalFromContext(ctx)
			if err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			if err := Enforce(ctx, principal, policy); err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			return next(ctx, req)
		}
	})
}
