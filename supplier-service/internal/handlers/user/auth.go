package user

import (
	"net/http"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/exterrors/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/deps"
	"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/views/userview"
	"github.com/jackc/pgx/v5"
	"github.com/pkg/errors"
)

const (
	ErrGetAuthClient = "failed to get firebase auth client"
	ErrInvalidToken  = "Token invalid"
	ErrUserNotFound  = "user not found"
)

func HandleAuthorizeUser(r *http.Request, env *deps.Env) (*api.Response, error) {
	var authview userview.AuthView
	if err := api.Decode(r, &authview); err != nil {
		return nil, errors.Wrap(err, "failed to decode request body")
	}

	auth, err := env.Firebase.Auth(r.Context())
	if err != nil {
		return nil, errors.Wrap(err, ErrGetAuthClient)
	}

	token, err := auth.VerifyIDToken(r.Context(), authview.UserToken)
	if err != nil {
		return nil, errs.WrapUnauthorizedError(err, ErrInvalidToken)
	}

	user, err := env.Queries.GetUserByUid(r.Context(), token.UID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errs.WrapNotFoundError(err, ErrUserNotFound)
		}
		return nil, errors.Wrap(err, "failed to get user by uid")
	}

	view := userview.ToUserView(&user)

	return api.NewResponse(view)
}
