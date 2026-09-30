package shared

import (
	"context"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/lifecycle"
	"google.golang.org/protobuf/proto"
)

type Principal func(context.Context) app.Caller

func WorkflowInterceptor(principal Principal, validator protovalidate.Validator) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			c := principal(ctx)
			if !c.Authenticated() {
				return nil, ConnectError(ctx, app.ErrUnauthenticated)
			}
			procedure := req.Spec().Procedure
			adminOnly := false
			switch procedure {
			case locationv1connect.LocationAdminServiceCreateLocationProcedure,
				locationv1connect.LocationAdminServiceUpdateLocationProcedure,
				locationv1connect.LocationAdminServiceArchiveLocationProcedure,
				locationv1connect.LocationAdminServiceUnarchiveLocationProcedure,
				locationv1connect.LocationDisablementServiceCreateDisablementProcedure,
				locationv1connect.LocationDisablementServiceListDisablementsProcedure,
				locationv1connect.LocationDisablementServiceUpdateDisablementProcedure,
				locationv1connect.LocationDisablementServiceEndDisablementProcedure,
				locationv1connect.LocationDisablementServiceCancelDisablementProcedure,
				locationv1connect.LocationAdditionRequestServiceApproveLocationAdditionRequestProcedure,
				locationv1connect.LocationAdditionRequestServiceRejectLocationAdditionRequestProcedure:
				adminOnly = true
			}
			if adminOnly && !c.IsAdmin() {
				return nil, ConnectError(ctx, app.ErrPermissionDenied)
			}
			if procedure == locationv1connect.LocationAdditionRequestServiceSubmitLocationAdditionRequestProcedure && c.Role == "suspended_user" {
				return nil, ConnectError(ctx, app.ErrPermissionDenied)
			}
			message, ok := req.Any().(proto.Message)
			if !ok {
				return nil, ConnectError(ctx, app.ErrInvalidArgument)
			}
			normalizeText(message)
			if err := validator.Validate(message); err != nil {
				return nil, ConnectError(ctx, app.ErrInvalidArgument)
			}
			response, err := next(ctx, req)
			return response, ConnectError(ctx, err)
		}
	})
}
