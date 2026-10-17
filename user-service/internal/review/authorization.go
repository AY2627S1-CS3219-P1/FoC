package review

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/tokenclaims"
	reviewv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/review/v1/reviewv1connect"
)

// AuthorizeInterceptor enforces token-claim RBAC per procedure after the
// request has been authenticated. It reads the Caller the HTTP middleware
// resolved and gates each procedure on the caller's role only. It never touches
// the request body and never performs a database lookup, so a role change takes
// effect on the next token without waiting for the DB-resolved authorization to
// re-resolve the actor.
func AuthorizeInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, ok := auth.CallerFromContext(ctx)
			if !ok {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			if err := authorizeCaller(caller, req.Spec().Procedure); err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			return next(ctx, req)
		}
	})
}

// authorizeCaller denies a procedure the caller's role cannot perform. Only
// procedures with a role gate are enumerated here; any procedure not matched is
// allowed for any authenticated caller.
func authorizeCaller(c auth.Caller, procedure string) error {
	switch procedure {
	case reviewv1connect.ReviewServiceSubmitReviewProcedure:
		// A suspended participant cannot rate the other side of their request.
		if c.Role == string(tokenclaims.RoleSuspendedUser) {
			return errs.NewForbiddenError("suspended users cannot submit reviews")
		}
	case reviewv1connect.ReviewServiceRemoveReviewProcedure, reviewv1connect.ReviewServiceListReviewRemovalsProcedure:
		if !c.Admin {
			return errs.NewForbiddenError("administrators only")
		}
	}
	return nil
}
