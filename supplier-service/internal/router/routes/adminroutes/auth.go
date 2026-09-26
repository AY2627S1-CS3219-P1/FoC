package adminroutes

import (
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/rest/user"
	"github.com/go-chi/chi/v5"
)

func SetupAuthRoutes(env *deps.Env) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/login", api.HTTPHandler(env, user.HandleAuthorizeUser))
	}
}
