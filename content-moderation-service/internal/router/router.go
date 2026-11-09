// Package router mounts CMS's RPCs on its two listeners.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	reviewv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/review/v1/reviewv1connect"
	moderationv1connect "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/moderation/v1/moderationv1connect"
)

// RPCServices contains RPC servers constructed at the composition root.
type RPCServices struct {
	Health                grpchealth.Checker
	ReviewModeration      reviewv1connect.ReviewModerationServiceHandler
	ReviewModerationAdmin reviewv1connect.ReviewModerationAdminServiceHandler
	ModerationAdmin       moderationv1connect.ModerationAdminServiceHandler
}

// Public mounts the health checks and the review moderation RPCs.
// Admin services are protected by RequireAdmin.
func Public(services RPCServices, authenticate func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(grpchealth.NewHandler(services.Health))

	reviewPath, reviewHandler := reviewv1connect.NewReviewModerationServiceHandler(
		services.ReviewModeration,
		connect.WithInterceptors(
			auth.RequireCaller(),
			validate.NewInterceptor(),
		),
	)
	mux.Handle(reviewPath, authenticate(reviewHandler))

	reviewAdminPath, reviewAdminHandler := reviewv1connect.NewReviewModerationAdminServiceHandler(
		services.ReviewModerationAdmin,
		connect.WithInterceptors(
			auth.RequireAdmin(),
			validate.NewInterceptor(),
		),
	)
	mux.Handle(reviewAdminPath, authenticate(reviewAdminHandler))

	moderationAdminPath, moderationAdminHandler := moderationv1connect.NewModerationAdminServiceHandler(
		services.ModerationAdmin,
		connect.WithInterceptors(
			auth.RequireAdmin(),
			validate.NewInterceptor(),
		),
	)
	mux.Handle(moderationAdminPath, authenticate(moderationAdminHandler))

	return mux
}

// Internal exposes only health; business RPCs require the public listener's authentication.
func Internal(services RPCServices) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(grpchealth.NewHandler(services.Health))
	return mux
}
