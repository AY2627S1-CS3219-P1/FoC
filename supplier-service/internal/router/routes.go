package router

import (
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/health"
	healthrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/health"
	adminrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/admin"
	discoveryrpc "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/discovery"
	"github.com/go-chi/chi/v5"
)

// SetupRoutes mounts the supplier RPCs and the legacy REST health route.
func SetupRoutes(r *chi.Mux, env *deps.Env, authenticator *httpauth.Authenticator, services Services) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(
		healthrpc.NewServer(),
	)
	r.Mount(healthPath, healthHandler)

	locationPath, locationHandler := locationv1connect.NewLocationDiscoveryServiceHandler(
		discoveryrpc.NewServer(services.LocationDiscovery),
		connect.WithInterceptors(auth.RequireCaller(), validate.NewInterceptor()),
	)
	r.Mount(locationPath, authenticator.Authenticate(locationHandler))

	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(
		adminrpc.NewServer(services.LocationAdmin),
		connect.WithInterceptors(auth.RequireAdmin(), validate.NewInterceptor()),
	)
	r.Mount(adminPath, authenticator.Authenticate(adminHandler))

	r.Route("/api", func(r chi.Router) {
		// Unprotected routes
		r.Get("/health", api.HTTPHandler(env, health.HandleCheckHealth))
		// Authentication is handled by User Service and the shared JWT middleware.
	})
}
