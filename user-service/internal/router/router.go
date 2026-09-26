// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	appmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router/routes"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/router/routes/adminroutes"
	userrpc "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/rpc"
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

// SetupRoutes mounts the user health RPC at its generated path and the public
// REST health and authentication routes under /api.
func SetupRoutes(r *chi.Mux, env *deps.Env) {
	healthPath, healthHandler := userv1connect.NewHealthServiceHandler(
		userrpc.NewHealthServer(),
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
