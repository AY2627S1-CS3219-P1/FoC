// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	userrpc "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/rpc"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Setup(env *deps.Env, auth *authhandler.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)

	healthPath, healthHandler := userv1connect.NewHealthServiceHandler(
		userrpc.NewHealthServer(),
	)
	r.Mount(healthPath, healthHandler)

	r.Get("/.well-known/jwks.json", api.HTTPHandler(env, auth.PublicKeys))
	r.Route("/api", func(r chi.Router) {
		r.Get("/health", api.HTTPHandler(env, health.HandleCheckHealth))
		r.Route("/auth", setupAuthRoutes(env, auth))
	})
	return r
}
