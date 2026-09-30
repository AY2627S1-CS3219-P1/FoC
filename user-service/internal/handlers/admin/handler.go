package admin

import (
	"context"
	"errors"
	"strconv"

	"connectrpc.com/connect"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
)

type Logic interface {
	GetUserByEmail(context.Context, uint, string) (models.User, error)
	ChangeUserRole(context.Context, uint, uint, models.RoleName, string) (models.User, error)
}

type Handler struct {
	userv1connect.UnimplementedUserAdminServiceHandler
	Logic Logic
}

func (h *Handler) GetUserByEmail(ctx context.Context, req *connect.Request[userv1.GetUserByEmailRequest]) (*connect.Response[userv1.GetUserByEmailResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	user, err := h.Logic.GetUserByEmail(ctx, actorID, req.Msg.Email)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	response := connect.NewResponse(&userv1.GetUserByEmailResponse{User: userdto.User(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) ChangeUserRole(ctx context.Context, req *connect.Request[userv1.ChangeUserRoleRequest]) (*connect.Response[userv1.ChangeUserRoleResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	targetID64, err := strconv.ParseUint(req.Msg.UserId, 10, strconv.IntSize)
	if err != nil || targetID64 == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user ID"))
	}
	targetID := uint(targetID64)
	to, ok := roleName(req.Msg.ToRole)
	if !ok {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid destination role"))
	}
	user, err := h.Logic.ChangeUserRole(ctx, actorID, targetID, to, req.Msg.Reason)
	if err != nil {
		return nil, userdto.MapError(err)
	}
	response := connect.NewResponse(&userv1.ChangeUserRoleResponse{User: userdto.User(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func roleName(role userv1.UserRole) (models.RoleName, bool) {
	switch role {
	case userv1.UserRole_USER_ROLE_ADMIN:
		return models.RoleAdmin, true
	case userv1.UserRole_USER_ROLE_USER:
		return models.RoleUser, true
	case userv1.UserRole_USER_ROLE_SUSPENDED_USER:
		return models.RoleSuspended, true
	default:
		return "", false
	}
}
