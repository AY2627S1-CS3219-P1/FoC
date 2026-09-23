// Package user contains handlers related to user operations.
package user

import (
	"net/http"

	"github.com/pkg/errors"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/api"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/views/userview"
)

const (
	ErrParseUserView = "Error parsing user view"
	ErrCreateUser    = "Error creating user"
)

func CreateUser(r *http.Request, env *api.Env) (*api.Response, error) {
	var req userview.CreateUserView
	if err := api.Decode(r, &req); err != nil {
		return nil, err
	}

	user, err := env.Queries.CreateUser(r.Context(), *req.ToCreateUserParams())
	if err != nil {
		return nil, errors.Wrap(err, ErrCreateUser)
	}

	return api.NewResponse(userview.ToUserView(&user))
}
