package report

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/report/v1/reportv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
)

// AuthorizeInterceptor gates each procedure on the caller's token-claim role,
// like the review interceptor, so a role change applies from the next token.
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

// authorizeCaller lists only the gated procedures. ListMyReports, GetReport and
// SubmitResponse are open to every authenticated caller, including suspended
// users, who keep access to existing reports (U6.8); the service checks that
// the caller is a participant.
func authorizeCaller(c userdto.Caller, procedure string) error {
	switch procedure {
	case reportv1connect.ReportServiceSubmitReportProcedure:
		if c.IsSuspended() {
			return errs.NewForbiddenError("suspended users cannot submit reports")
		}
	case reportv1connect.ReportServiceListReportsProcedure,
		reportv1connect.ReportServiceBeginReviewProcedure,
		reportv1connect.ReportServiceRequestResponseProcedure,
		reportv1connect.ReportServiceCloseReportProcedure:
		if !c.Admin {
			return errs.NewForbiddenError("administrators only")
		}
	}
	return nil
}
