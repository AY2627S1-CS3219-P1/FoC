package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/health"
	"github.com/go-chi/chi/v5"
)

// SetupRoutes mounts the supplier RPCs and the legacy REST health route.
func SetupRoutes(r *chi.Mux, env *deps.Env, authenticator *httpauth.Authenticator, services RPCServices) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(services.Health)
	r.Mount(healthPath, healthHandler)
	MountLocationServices(r, authenticator.Authenticate, services)

	r.Route("/api", func(r chi.Router) {
		// Unprotected routes
		r.Get("/health", api.HTTPHandler(env, health.HandleCheckHealth))
		// Authentication is handled by User Service and the shared JWT middleware.
	})
}

// MountLocationServices mounts each implemented capability exactly once.
func MountLocationServices(r chi.Router, authenticate func(http.Handler) http.Handler, services RPCServices) {
	locationPath, locationHandler := locationv1connect.NewLocationDiscoveryServiceHandler(services.Discovery,
		connect.WithInterceptors(auth.RequireCaller(), validate.NewInterceptor()))
	r.Mount(locationPath, authenticate(locationHandler))
	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(services.Admin,
		connect.WithInterceptors(auth.RequireAdmin(), validate.NewInterceptor()))
	r.Mount(adminPath, authenticate(adminHandler))
	options := connect.WithInterceptors(services.DisablementInterceptor)
	disablementPath, disablementHandler := locationv1connect.NewLocationDisablementServiceHandler(services.Disablement, options)
	r.Mount(disablementPath, authenticate(disablementHandler))
	requestPath, requestHandler := locationv1connect.NewLocationAdditionRequestServiceHandler(services.AdditionRequest, connect.WithInterceptors(services.AdditionRequestInterceptor))
	r.Mount(requestPath, authenticate(requestHandler))
}
