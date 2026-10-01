package router

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
)

type ActorReader interface {
	GetByID(context.Context, uuid.UUID) (*models.User, error)
}

func authorizeProtected(users ActorReader) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			caller, ok := auth.CallerFromContext(ctx)
			if !ok {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			actorID, err := uuid.Parse(caller.ID)
			if err != nil || actorID == uuid.Nil {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			actor, err := users.GetByID(ctx, actorID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			if err != nil {
				return nil, api.ToConnectError(ctx, fmt.Errorf("resolve authenticated user: %w", err))
			}
			switch req.Spec().Procedure {
			case "/user.v1.ProfileService/UpdateMyProfile":
				if actor.Role == models.RoleSuspended {
					return nil, api.ToConnectError(ctx, errs.NewForbiddenError("permission denied"))
				}
			case "/user.v1.UserAdminService/GetUserByEmail", "/user.v1.UserAdminService/ChangeUserRole":
				if !actor.Role.IsAdmin() {
					return nil, api.ToConnectError(ctx, errs.NewForbiddenError("permission denied"))
				}
			}
			return next(ctx, req)
		}
	})
}
