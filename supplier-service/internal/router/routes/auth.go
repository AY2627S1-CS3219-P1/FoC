package routes

import (
	"github.com/go-chi/chi/v5"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/api"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/handlers/user"
)

func SetupAuthRoutes(env *api.Env) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", api.HTTPHandler(env, user.HandleAuthorizeUser))
		r.Post("/create", api.HTTPHandler(env, user.CreateUser))
	}
}
