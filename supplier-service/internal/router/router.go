// Package router sets up the HTTP router with middleware and routes.
package router

import (
	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth/httpauth"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/location/v1/locationv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/supplier/v1/supplierv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
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

// RPCServices contains dependencies constructed at the application composition root.
type RPCServices struct {
	Health                     supplierv1connect.HealthServiceHandler
	Discovery                  locationv1connect.LocationDiscoveryServiceHandler
	Admin                      locationv1connect.LocationAdminServiceHandler
	Disablement                locationv1connect.LocationDisablementServiceHandler
	AdditionRequest            locationv1connect.LocationAdditionRequestServiceHandler
	DisablementInterceptor     connect.Interceptor
	AdditionRequestInterceptor connect.Interceptor
}
