package adminroutes

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/go-chi/chi/v5"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/user"
)

func SetupAuthRoutes(env *deps.Env) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/login", api.HTTPHandler(env, user.HandleAuthorizeUser))
	}
}
