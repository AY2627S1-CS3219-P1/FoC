package appeal

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/actor"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/appeal/v1/appealv1connect"
)

// AuthorizeInterceptor gates each procedure on the caller's token-claim role,
// like the report interceptor.
func AuthorizeInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, err := actor.From(ctx)
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

// authorizeCaller gates only the administrator procedures. Every participant
// procedure is open to suspended users, who may appeal the decisions that
// affect them (U6.8.1); the service checks that the caller is affected.
func authorizeCaller(c actor.Caller, procedure string) error {
	switch procedure {
	case appealv1connect.AppealServiceListAppealsProcedure,
		appealv1connect.AppealServiceBeginReviewProcedure,
		appealv1connect.AppealServiceRequestResponseProcedure,
		appealv1connect.AppealServiceCloseAppealProcedure:
		if !c.Admin {
			return errs.NewForbiddenError("administrators only")
		}
	}
	return nil
}
