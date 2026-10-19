package review

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	reviewv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
)

// AuthorizeInterceptor gates each procedure on the caller's token-claim role.
// It never touches the request body and never performs a database lookup, so a
// role change takes effect on the next token rather than immediately.
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

// authorizeCaller enumerates only the procedures with a role gate; any
// procedure not matched is allowed for any authenticated caller.
func authorizeCaller(c userdto.Caller, procedure string) error {
	switch procedure {
	case reviewv1connect.ReviewServiceSubmitReviewProcedure:
		if c.IsSuspended() {
			return errs.NewForbiddenError("suspended users cannot submit reviews")
		}
	case reviewv1connect.ReviewServiceRemoveReviewProcedure, reviewv1connect.ReviewServiceListReviewRemovalsProcedure:
		if !c.Admin {
			return errs.NewForbiddenError("administrators only")
		}
	}
	return nil
}
