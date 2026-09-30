package router

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
)

type ActorReader interface {
	GetByID(context.Context, uint) (*models.User, error)
}

func authorizeProtected(users ActorReader) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			claims, ok := authmiddleware.ClaimsFromContext[authmiddleware.AccessClaims](ctx)
			if !ok {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access token"))
			}
			actorID64, err := strconv.ParseUint(claims.Subject, 10, strconv.IntSize)
			if err != nil || actorID64 == 0 {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access token"))
			}
			actorID := uint(actorID64)
			actor, err := users.GetByID(ctx, actorID)
			if errors.Is(err, store.ErrNotFound) {
				return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid or missing access token"))
			}
			if err != nil {
				return nil, fmt.Errorf("resolve authenticated user: %w", err)
			}
			switch req.Spec().Procedure {
			case "/user.v1.ProfileService/UpdateMyProfile":
				if actor.Role == models.RoleSuspended {
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
				}
			case "/user.v1.UserAdminService/GetUserByEmail", "/user.v1.UserAdminService/ChangeUserRole":
				if !actor.Role.IsAdmin() {
					return nil, connect.NewError(connect.CodePermissionDenied, errors.New("permission denied"))
				}
			}
			return next(ctx, req)
		}
	})
}
