// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	locationadmin "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/admin"
	discovery "github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/location/discovery"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Services contains the domain services exposed by the router.
type Services struct {
	LocationAdmin     *locationadmin.Service
	LocationDiscovery *discovery.Service
}

func Setup(env *deps.Env, authenticator *httpauth.Authenticator, services Services) *chi.Mux {
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
