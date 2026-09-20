// Package user contains handlers related to user operations.
package user

import (
	"net/http"

	"github.com/pkg/errors"
	"github.com/yihao03/reminding/internal/api"
	"github.com/yihao03/reminding/internal/views/userview"
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
