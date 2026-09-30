package userdto

import (
	"context"
	"errors"
	"strconv"

	userv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/user/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	authmiddleware "github.com/AY2627S1-CS3219-P1/FoC/user-service/pkg/middleware"
)

var ErrMissingActor = errors.New("missing authenticated actor")

func ActorID(ctx context.Context) (uint, error) {
	claims, ok := authmiddleware.ClaimsFromContext[authmiddleware.AccessClaims](ctx)
	if !ok {
		return 0, ErrMissingActor
	}
	id, err := strconv.ParseUint(claims.Subject, 10, strconv.IntSize)
	if err != nil || id == 0 {
		return 0, ErrMissingActor
	}
	return uint(id), nil
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
	return &userv1.User{Id: strconv.FormatUint(uint64(user.ID), 10), Email: user.Email, DisplayName: user.DisplayName,
		Role: Role(user.Role), TelegramHandle: user.TelegramHandle, PhoneNumber: user.PhoneNumber}
}

// UserSummary omits contact details when an administrator inspects another user.
func UserSummary(user models.User) *userv1.UserSummary {
	return &userv1.UserSummary{Id: strconv.FormatUint(uint64(user.ID), 10), Email: user.Email,
		DisplayName: user.DisplayName, Role: Role(user.Role)}
}

func Profile(user models.User) *userv1.Profile {
	return &userv1.Profile{Id: strconv.FormatUint(uint64(user.ID), 10), Email: user.Email, DisplayName: user.DisplayName,
		Description: user.Description, TelegramHandle: user.TelegramHandle,
		PhoneNumber: user.PhoneNumber, Role: Role(user.Role)}
}
