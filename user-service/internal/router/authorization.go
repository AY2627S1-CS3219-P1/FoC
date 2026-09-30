package router

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/google/uuid"
)

func authorizeProtected(policies map[string]authorization.Policy) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			policy, ok := policies[req.Spec().Procedure]
			if !ok {
				return nil, api.ToConnectError(ctx, errors.New("protected RPC has no authorization policy"))
			}
			claims, ok := authmiddleware.ClaimsFromContext[authmiddleware.AccessClaims](ctx)
			if !ok {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			actorID, err := uuid.Parse(claims.Subject)
			if err != nil || actorID == uuid.Nil {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			if err := authorization.Enforce(ctx, claims, policy); err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			return next(ctx, req)
		}
	})
}
