// Package router sets up the HTTP router with middleware and Connect services.
package router

import (
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	healthhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const maxRPCMessageBytes = 1 << 20

func Setup(auth *authhandler.Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(middleware.Recoverer)

	healthPath, healthHandler := userv1connect.NewHealthServiceHandler(
		healthhandler.New(),
	)
	r.Mount(healthPath, healthHandler)

	authPath, authService := userv1connect.NewAuthServiceHandler(
		auth,
		connect.WithInterceptors(validate.NewInterceptor(), normalizeRPCError()),
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	r.With(checkOrigin(auth.AllowedOrigin)).Mount(authPath, authService)

	keysPath, keysService := userv1connect.NewPublicKeyServiceHandler(
		auth,
		connect.WithInterceptors(normalizeRPCError()),
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	r.Mount(keysPath, keysService)
	return r
}
