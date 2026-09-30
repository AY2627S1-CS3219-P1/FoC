// Package router sets up the HTTP router with middleware and Connect services.
package router

import (
	"net/http"

	"connectrpc.com/connect"
	"connectrpc.com/validate"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	sharedmiddleware "github.com/AY2627S1-CS3219-P1/FoC/pkg/middleware"
	adminhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/admin"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	healthhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/health"
	profilehandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/profile"
	userservicemiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/middleware"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

const maxRPCMessageBytes = 1 << 20

// Setup mounts the Connect handlers built in main with their dependencies set.
type ProtectedRoutes struct {
	Profile             *profilehandler.Handler
	Admin               *adminhandler.Handler
	Authenticate        func(http.Handler) http.Handler
	Users               ActorReader
	ProfileReadPolicy   authorization.Policy
	ProfileUpdatePolicy authorization.Policy
	AdminPolicy         authorization.Policy
}

func Setup(health *healthhandler.Handler, auth *authhandler.Handler, protected ...ProtectedRoutes) *chi.Mux {
	r := chi.NewRouter()
	r.Use(chimiddleware.RequestID)
	r.Use(chimiddleware.RealIP)
	r.Use(sharedmiddleware.RequestLogger)
	r.Use(chimiddleware.Recoverer)

	healthPath, healthHandler := userv1connect.NewHealthServiceHandler(
		health,
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	r.Mount(healthPath, healthHandler)

	authPath, authService := userv1connect.NewAuthServiceHandler(
		auth,
		connect.WithInterceptors(validate.NewInterceptor(), normalizeRPCError()),
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	r.With(userservicemiddleware.CheckOrigin(auth.AllowedOrigin)).Mount(authPath, authService)

	keysPath, keysService := userv1connect.NewPublicKeyServiceHandler(
		auth,
		connect.WithInterceptors(normalizeRPCError()),
		connect.WithReadMaxBytes(maxRPCMessageBytes),
	)
	r.Mount(keysPath, keysService)

	if len(protected) > 0 {
		p := protected[0]
		policies := map[string]authorization.Policy{
			userv1connect.ProfileServiceGetMyProfileProcedure:     p.ProfileReadPolicy,
			userv1connect.ProfileServiceUpdateMyProfileProcedure:  p.ProfileUpdatePolicy,
			userv1connect.UserAdminServiceGetUserByEmailProcedure: p.AdminPolicy,
			userv1connect.UserAdminServiceChangeUserRoleProcedure: p.AdminPolicy,
		}
		interceptors := connect.WithInterceptors(normalizeRPCError(), authorizeProtected(p.Users, policies), validate.NewInterceptor())
		profilePath, profileService := userv1connect.NewProfileServiceHandler(
			p.Profile, interceptors, connect.WithReadMaxBytes(maxRPCMessageBytes))
		r.With(userservicemiddleware.CheckOrigin(auth.AllowedOrigin), p.Authenticate).Mount(profilePath, profileService)
		adminPath, adminService := userv1connect.NewUserAdminServiceHandler(
			p.Admin, interceptors, connect.WithReadMaxBytes(maxRPCMessageBytes))
		r.With(userservicemiddleware.CheckOrigin(auth.AllowedOrigin), p.Authenticate).Mount(adminPath, adminService)
	}
	return r
}
