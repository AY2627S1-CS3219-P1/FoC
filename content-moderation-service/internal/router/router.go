// Package router mounts CMS's RPCs on its two listeners.
//
// The public listener serves users, authenticated by their User Service JWT.
// The internal listener serves other services (not yet used).
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

// Public mounts the health check and the review moderation RPCs.
// Admin services are protected by RequireAdmin.
func Public(services RPCServices, authenticate func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(grpchealth.NewHandler(services.Health))

	// ReviewModerationService: owner-or-admin, caller-gated
	reviewPath, reviewHandler := reviewv1connect.NewReviewModerationServiceHandler(
		services.ReviewModeration,
		connect.WithInterceptors(
			auth.RequireCaller(),
			validate.NewInterceptor(),
		),
	)
	mux.Handle(reviewPath, authenticate(reviewHandler))

	// ReviewModerationAdminService: admin-only, admin-gated
	reviewAdminPath, reviewAdminHandler := reviewv1connect.NewReviewModerationAdminServiceHandler(
		services.ReviewModerationAdmin,
		connect.WithInterceptors(
			auth.RequireAdmin(),
			validate.NewInterceptor(),
		),
	)
	mux.Handle(reviewAdminPath, authenticate(reviewAdminHandler))

	// ModerationAdminService: admin-only, admin-gated
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

// Internal mounts the health check for service-to-service calls.
// It does not verify authentication yet, so it must only be reachable from
// inside the service network.
func Internal(services RPCServices) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle(grpchealth.NewHandler(services.Health))

	// ReviewModerationService: owner-or-admin
	reviewPath, reviewHandler := reviewv1connect.NewReviewModerationServiceHandler(
		services.ReviewModeration,
		connect.WithInterceptors(
			validate.NewInterceptor(),
		),
	)
	mux.Handle(reviewPath, reviewHandler)

	// ReviewModerationAdminService: admin-only
	reviewAdminPath, reviewAdminHandler := reviewv1connect.NewReviewModerationAdminServiceHandler(
		services.ReviewModerationAdmin,
		connect.WithInterceptors(
			validate.NewInterceptor(),
		),
	)
	mux.Handle(reviewAdminPath, reviewAdminHandler)

	// ModerationAdminService: admin-only
	moderationAdminPath, moderationAdminHandler := moderationv1connect.NewModerationAdminServiceHandler(
		services.ModerationAdmin,
		connect.WithInterceptors(
			validate.NewInterceptor(),
		),
	)
	mux.Handle(moderationAdminPath, moderationAdminHandler)

	return mux
}
