package warning

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
)

// AuthorizeInterceptor applies warning-specific role gates from the caller's
// token claims, matching the User Service review authorization behavior.
func AuthorizeInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, err := userdto.Actor(ctx)
			if err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			if err := authorizeCaller(caller, req.Spec().Procedure); err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			return next(ctx, req)
		}
	})
}

func authorizeCaller(caller userdto.Caller, procedure string) error {
	switch procedure {
	case userv1connect.WarningServiceListWarningsForUserProcedure,
		userv1connect.WarningServiceMarkWarningAsRemovedProcedure:
		if !caller.Admin {
			return errs.NewForbiddenError("administrators only")
		}
	}
	return nil
}
