// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/locationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	locationadmin "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/admin"
	discovery "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/health"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/health"
	adminrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/admin"
	discoveryrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/discovery"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Setup(env *deps.Env, authenticator *httpauth.Authenticator, locationAdmin *locationadmin.Service) *chi.Mux {
	r := chi.NewRouter()

	SetupMiddleware(r)
	SetupRoutes(r, env, authenticator, locationAdmin)
	return r
}

func SetupMiddleware(r *chi.Mux) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)
}

// SetupRoutes mounts the supplier RPCs and the legacy REST health route.
func SetupRoutes(r *chi.Mux, env *deps.Env, authenticator *httpauth.Authenticator, locationAdmin *locationadmin.Service) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(
		healthrpc.NewHealthServer(),
	)
	r.Mount(healthPath, healthHandler)

	locationPath, locationHandler := locationv1connect.NewLocationDiscoveryServiceHandler(
		discoveryrpc.NewLocationServer(discovery.NewService(
			discovery.NewPostgresReader(locationdb.New(env.Pool)),
		)),
		connect.WithInterceptors(auth.RequireCaller(), validate.NewInterceptor()),
	)
	r.Mount(locationPath, authenticator.Authenticate(locationHandler))

	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(
		adminrpc.NewServer(locationAdmin),
		connect.WithInterceptors(auth.RequireAdmin(), validate.NewInterceptor()),
	)
	r.Mount(adminPath, authenticator.Authenticate(adminHandler))

	r.Route("/api", func(r chi.Router) {
		// Unprotected routes
		r.Get("/health", api.HTTPHandler(env, health.HandleCheckHealth))
		// Authentication is handled by User Service and the shared JWT middleware.
	})
}
