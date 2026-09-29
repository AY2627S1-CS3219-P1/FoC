// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
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
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Setup(env *deps.Env) *chi.Mux {
	r := chi.NewRouter()

	SetupMiddleware(r)
	SetupRoutes(r, env)
	SetupAdminRoutes(r, env)
	return r
}

func SetupMiddleware(r *chi.Mux) {
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)
}

// SetupRoutes mounts the supplier health and Location RPCs at their generated
// paths and the public REST health and authentication routes under /api.
func SetupRoutes(r *chi.Mux, env *deps.Env) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(
		supplierrpc.NewHealthServer(),
	)
	r.Mount(healthPath, healthHandler)

	locationPath, locationHandler := supplierv1connect.NewLocationServiceHandler(
		supplierrpc.NewLocationServer(location.NewService(
			location.NewPostgresReader(locationdb.New(env.Pool)),
		)),
		connect.WithInterceptors(validate.NewInterceptor()),
	)
	r.Mount(locationPath, locationHandler)

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
