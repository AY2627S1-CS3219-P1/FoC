// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/health"
	appmiddleware "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router/routes"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router/routes/adminroutes"
	supplierrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Setup(env *deps.Env, authenticator *authmiddleware.Authenticator, locationAdmin *location.AdminService, adminPolicy authorization.Policy) *chi.Mux {
	r := chi.NewRouter()

	SetupMiddleware(r)
	SetupRoutes(r, env, authenticator, locationAdmin, adminPolicy)
	SetupAdminRoutes(r, env)
	return r
}

func SetupMiddleware(r *chi.Mux) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)
}

// SetupRoutes mounts the supplier health RPC at its generated path and the
// public REST health and authentication routes under /api.
func SetupRoutes(r *chi.Mux, env *deps.Env, authenticator *authmiddleware.Authenticator, locationAdmin *location.AdminService, adminPolicy authorization.Policy) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(
		supplierrpc.NewHealthServer(),
	)
	r.Mount(healthPath, healthHandler)

	locationPath, locationHandler := locationv1connect.NewLocationDiscoveryServiceHandler(
		supplierrpc.NewLocationServer(location.NewService(
			location.NewPostgresReader(locationdb.New(env.Pool)),
		)),
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	r.Mount(locationPath, authenticator.Authenticate(locationHandler))

	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(
		supplierrpc.NewLocationAdminServer(locationAdmin),
		connect.WithInterceptors(supplierrpc.AdminAuthorizationInterceptor(adminPolicy), validate.NewInterceptor()),
	)
	r.Mount(adminPath, authenticator.Authenticate(adminHandler))

	r.Route("/api", func(r chi.Router) {
		// Unprotected routes
		r.Get("/health", api.HTTPHandler(env, health.HandleCheckHealth))
		r.Route("/auth", routes.SetupAuthRoutes(env))

		// Protected routes
		r.Route("/", func(r chi.Router) {
			r.Use(appmiddleware.GetAuthMiddleware(env))
		})
	})
}

func SetupAdminRoutes(r chi.Router, env *deps.Env) {
	r.Route("/api/admin", func(r chi.Router) {
		// Unprotected routes
		r.Route("/auth", adminroutes.SetupAuthRoutes(env))

		// Protected routes
		r.Route("/", func(r chi.Router) {
			r.Use(appmiddleware.GetAuthMiddleware(env))
		})
	})
}
