package shared

import (
	"context"

	"buf.build/go/protovalidate"
	"connectrpc.com/connect"
	app "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/shared"
	"google.golang.org/protobuf/proto"
)

type Principal func(context.Context) app.Caller

func CallerInterceptor(principal Principal, validator protovalidate.Validator, authorize func(app.Caller, string) error) connect.Interceptor {
	if principal == nil || validator == nil || authorize == nil {
		panic("caller interceptor dependencies are required")
	}
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			c := principal(ctx)
			if !c.Authenticated() {
				return nil, ConnectError(ctx, app.ErrUnauthenticated)
			}
			if err := authorize(c, req.Spec().Procedure); err != nil {
				return nil, ConnectError(ctx, err)
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
