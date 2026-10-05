// Package router sets up the HTTP router with middleware and routes.
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
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/health"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Setup(env *deps.Env, authenticator *httpauth.Authenticator, services RPCServices) *chi.Mux {
	r := chi.NewRouter()

	SetupMiddleware(r)
	SetupRoutes(r, env, authenticator, services)
	return r
}

func SetupMiddleware(r *chi.Mux) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)
}

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

// RPCServices contains dependencies constructed at the application composition root.
type RPCServices struct {
	Health              supplierv1connect.HealthServiceHandler
	Discovery           locationv1connect.LocationDiscoveryServiceHandler
	Admin               locationv1connect.LocationAdminServiceHandler
	Disablement         locationv1connect.LocationDisablementServiceHandler
	AdditionRequest     locationv1connect.LocationAdditionRequestServiceHandler
	WorkflowInterceptor connect.Interceptor
}

// MountLocationServices mounts each implemented capability exactly once.
func MountLocationServices(r chi.Router, authenticate func(http.Handler) http.Handler, services RPCServices) {
	locationPath, locationHandler := locationv1connect.NewLocationDiscoveryServiceHandler(services.Discovery,
		connect.WithInterceptors(auth.RequireCaller(), validate.NewInterceptor()))
	r.Mount(locationPath, authenticate(locationHandler))
	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(services.Admin,
		connect.WithInterceptors(auth.RequireAdmin(), validate.NewInterceptor()))
	r.Mount(adminPath, authenticate(adminHandler))
	options := connect.WithInterceptors(services.WorkflowInterceptor)
	disablementPath, disablementHandler := locationv1connect.NewLocationDisablementServiceHandler(services.Disablement, options)
	r.Mount(disablementPath, authenticate(disablementHandler))
	requestPath, requestHandler := locationv1connect.NewLocationAdditionRequestServiceHandler(services.AdditionRequest, options)
	r.Mount(requestPath, authenticate(requestHandler))
}
