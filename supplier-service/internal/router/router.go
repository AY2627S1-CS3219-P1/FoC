// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/health"
	appmiddleware "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router/routes"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/router/routes/adminroutes"
	rpcshared "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rpc/location/shared"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Setup(env *deps.Env, authenticator *authmiddleware.Authenticator, services RPCServices) *chi.Mux {
	r := chi.NewRouter()

	SetupMiddleware(r)
	SetupRoutes(r, env, authenticator, services)
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
func SetupRoutes(r *chi.Mux, env *deps.Env, authenticator *authmiddleware.Authenticator, services RPCServices) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(
		services.Health,
	)
	r.Mount(healthPath, healthHandler)

	MountLocationServices(r, authenticator.Authenticate, services)

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
		connect.WithInterceptors(validate.NewInterceptor()))
	r.Mount(locationPath, authenticate(locationHandler))
	adminPath, adminHandler := locationv1connect.NewLocationAdminServiceHandler(services.Admin,
		connect.WithInterceptors(rpcshared.AdminAuthorizationInterceptor(), validate.NewInterceptor()))
	r.Mount(adminPath, authenticate(adminHandler))
	options := connect.WithInterceptors(services.WorkflowInterceptor)
	disablementPath, disablementHandler := locationv1connect.NewLocationDisablementServiceHandler(services.Disablement, options)
	r.Mount(disablementPath, authenticate(disablementHandler))
	requestPath, requestHandler := locationv1connect.NewLocationAdditionRequestServiceHandler(services.AdditionRequest, options)
	r.Mount(requestPath, authenticate(requestHandler))
}
