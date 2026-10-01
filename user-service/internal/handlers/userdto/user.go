package userdto

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/google/uuid"
)

var ErrMissingActor = errs.NewUnauthorizedError("invalid or missing access token")

func ActorID(ctx context.Context) (uuid.UUID, error) {
	caller, ok := auth.CallerFromContext(ctx)
	if !ok {
		return uuid.Nil, ErrMissingActor
	}
	id, err := uuid.Parse(caller.ID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, ErrMissingActor
	}
	return id, nil
}

func Role(role models.RoleName) userv1.UserRole {
	switch role {
	case models.RoleSuperAdmin:
		return userv1.UserRole_USER_ROLE_SUPER_ADMIN
	case models.RoleAdmin:
		return userv1.UserRole_USER_ROLE_ADMIN
	case models.RoleUser:
		return userv1.UserRole_USER_ROLE_USER
	case models.RoleSuspended:
		return userv1.UserRole_USER_ROLE_SUSPENDED_USER
	default:
		return userv1.UserRole_USER_ROLE_UNSPECIFIED
	}
}

func User(user models.User) *userv1.User {
	return &userv1.User{Id: user.ID.String(), Email: user.Email, DisplayName: user.DisplayName,
		Role: Role(user.Role), TelegramHandle: user.TelegramHandle, PhoneNumber: user.PhoneNumber}
}

// UserSummary omits contact details when an administrator inspects another user.
func UserSummary(user models.User) *userv1.UserSummary {
	return &userv1.UserSummary{Id: user.ID.String(), Email: user.Email,
		DisplayName: user.DisplayName, Role: Role(user.Role)}
}

func Profile(user models.User) *userv1.Profile {
	return &userv1.Profile{Id: user.ID.String(), Email: user.Email, DisplayName: user.DisplayName,
		Description: user.Description, TelegramHandle: user.TelegramHandle,
		PhoneNumber: user.PhoneNumber, Role: Role(user.Role)}
}
