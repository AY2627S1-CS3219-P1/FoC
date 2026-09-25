// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/foc/supplier/v1/supplierv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
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

func SetupRoutes(r *chi.Mux, env *deps.Env) {
	healthPath, healthHandler := supplierv1connect.NewHealthServiceHandler(
		supplierrpc.NewHealthServer(),
	)
	r.Mount(healthPath, healthHandler)

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
