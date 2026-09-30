package userdto

import (
	"errors"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/jwt"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/service"
)

func MapError(err error) error {
	switch {
	case errors.Is(err, ErrMissingActor), errors.Is(err, service.ErrUnauthenticated):
		return connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access token"))
	case errors.Is(err, service.ErrPermissionDenied):
		return connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
	case errors.Is(err, service.ErrInvalidProfile), errors.Is(err, service.ErrInvalidRoleChange),
		errors.Is(err, jwt.ErrInvalidEmail):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.Is(err, service.ErrUserNotFound):
		return connect.NewError(connect.CodeNotFound, err)
	case errors.Is(err, service.ErrRoleUnchanged):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, service.ErrConcurrentRoleEdit):
		return connect.NewError(connect.CodeAborted, err)
	default:
		return err
	}
}
