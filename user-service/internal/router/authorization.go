package router

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/authorization"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
	"github.com/google/uuid"
)

type ActorReader interface {
	GetByID(context.Context, uuid.UUID) (*models.User, error)
}

type actorPrincipal struct{ user *models.User }

func (p actorPrincipal) SubjectID() string { return p.user.ID.String() }
func (p actorPrincipal) RoleName() authorization.Role {
	return authorization.Role(p.user.Role)
}

func authorizeProtected(users ActorReader, policies map[string]authorization.Policy) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			policy, ok := policies[req.Spec().Procedure]
			if !ok {
				return nil, api.ToConnectError(ctx, errors.New("protected RPC has no authorization policy"))
			}
			claims, ok := authmiddleware.ClaimsFromContext[authmiddleware.AccessClaims](ctx)
			if !ok {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			actorID, err := uuid.Parse(claims.Subject)
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
			if actor == nil {
				return nil, api.ToConnectError(ctx, errs.NewUnauthorizedError("invalid or missing access token"))
			}
			if err := authorization.Enforce(ctx, actorPrincipal{user: actor}, policy); err != nil {
				return nil, api.ToConnectError(ctx, err)
			}
			return next(ctx, req)
		}
	})
}
