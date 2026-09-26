package router

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	authhandler "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/auth"
	"github.com/go-chi/chi/v5"
)

func setupAuthRoutes(env *deps.Env, auth *authhandler.Handler) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", api.HTTPHandler(env, auth.RequestLink))
		r.Post("/login", api.HTTPHandler(env, auth.Login))
		r.Post("/register", api.HTTPHandler(env, auth.Register))
		r.Post("/refresh", api.HTTPHandler(env, auth.Refresh))
		r.Post("/logout", api.HTTPHandler(env, auth.Logout))
	}
}
