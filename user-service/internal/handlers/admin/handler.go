package admin

import (
	"context"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1/userv1connect"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/handlers/userdto"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

type Logic interface {
	GetUserByEmail(context.Context, uuid.UUID, string) (models.User, error)
	ChangeUserRole(context.Context, uuid.UUID, uuid.UUID, models.RoleName, string) (models.User, error)
}

type Handler struct {
	userv1connect.UnimplementedUserAdminServiceHandler
	Logic    Logic
	Deletion DeletionLogic
}

type DeletionLogic interface {
	DeleteUser(context.Context, uuid.UUID, uuid.UUID) error
}

func (h *Handler) DeleteUser(
	ctx context.Context,
	req *connect.Request[userv1.DeleteUserRequest],
) (*connect.Response[userv1.DeleteUserResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	targetID, err := uuid.Parse(req.Msg.UserId)
	if err != nil || targetID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid user ID"))
	}
	if h.Deletion == nil {
		return nil, api.ToConnectError(ctx, errs.NewUnavailableError("Account deletion is unavailable."))
	}
	if err := h.Deletion.DeleteUser(ctx, actorID, targetID); err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.DeleteUserResponse{})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) GetUserByEmail(
	ctx context.Context,
	req *connect.Request[userv1.GetUserByEmailRequest],
) (*connect.Response[userv1.GetUserByEmailResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	user, err := h.Logic.GetUserByEmail(ctx, actorID, req.Msg.Email)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.GetUserByEmailResponse{User: userdto.UserSummary(user)})
	response.Header().Set("Cache-Control", "no-store")
	return response, nil
}

func (h *Handler) ChangeUserRole(
	ctx context.Context,
	req *connect.Request[userv1.ChangeUserRoleRequest],
) (*connect.Response[userv1.ChangeUserRoleResponse], error) {
	actorID, err := userdto.ActorID(ctx)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	targetID, err := uuid.Parse(req.Msg.UserId)
	if err != nil || targetID == uuid.Nil {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid user ID"))
	}
	to, ok := roleName(req.Msg.ToRole)
	if !ok {
		return nil, api.ToConnectError(ctx, errs.NewBadRequestError("invalid destination role"))
	}
	user, err := h.Logic.ChangeUserRole(ctx, actorID, targetID, to, req.Msg.Reason)
	if err != nil {
		return nil, api.ToConnectError(ctx, err)
	}
	response := connect.NewResponse(&userv1.ChangeUserRoleResponse{User: userdto.UserSummary(user)})
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

