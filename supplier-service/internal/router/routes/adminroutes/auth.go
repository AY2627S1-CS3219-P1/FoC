package adminroutes

import (
	"github.com/go-chi/chi/v5"
	"github.com/yihao03/reminding/internal/api"
	"github.com/yihao03/reminding/internal/handlers/user"
)

func SetupAuthRoutes(env *api.Env) func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/login", api.HTTPHandler(env, user.HandleAuthorizeUser))
	}
}
