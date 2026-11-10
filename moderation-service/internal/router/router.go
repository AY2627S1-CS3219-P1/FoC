// Package router mounts Moderation Service's Connect handlers.
package router

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/appeal"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/health"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/report"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/appeal/v1/appealv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/report/v1/reportv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1/moderationv1connect"
)

// maxRPCMessageBytes caps request bodies; evidence images never pass through.
const maxRPCMessageBytes = 1 << 20

// Services are the handlers the router mounts.
type Services struct {
	Health *health.Handler
	Report *report.Handler
	Appeal *appeal.Handler
}

// Public serves the frontend: health is open, and every report and appeal
// RPC requires a verified caller.
func Public(services Services, authenticate func(http.Handler) http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle(moderationv1connect.NewHealthServiceHandler(services.Health))

	reportPath, reportHandler := reportv1connect.NewReportServiceHandler(
		services.Report,
		connect.WithInterceptors(validate.NewInterceptor(), report.AuthorizeInterceptor(), normalizeRPCError()),
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	mux.Handle(reportPath, authenticate(reportHandler))

	appealPath, appealHandler := appealv1connect.NewAppealServiceHandler(
		services.Appeal,
		connect.WithInterceptors(validate.NewInterceptor(), appeal.AuthorizeInterceptor(), normalizeRPCError()),
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	mux.Handle(appealPath, authenticate(appealHandler))
	return mux
}

// Internal serves the internal listener. Moderation Service has no private
// RPCs yet, so it serves the public routes there too (CONVENTIONS).
func Internal(services Services, authenticate func(http.Handler) http.Handler) http.Handler {
	return Public(services, authenticate)
}

// normalizeRPCError maps domain errors that are not already Connect errors to
// their external codes, and hides internal ones.
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
			return nil, api.ToConnectError(ctx, err)
		}
	})
}
