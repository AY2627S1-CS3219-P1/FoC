package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
	"github.com/google/uuid"
	"github.com/nyaruka/phonenumbers"
)

var (
	ErrUnauthenticated    = errs.NewUnauthorizedError("invalid or missing access token")
	ErrPermissionDenied   = errs.NewForbiddenError("permission denied")
	ErrInvalidProfile     = errs.NewBadRequestError("invalid profile")
	ErrUserNotFound       = errs.NewNotFoundError("user not found")
	ErrInvalidRoleChange  = errs.NewBadRequestError("invalid role change")
	ErrRoleUnchanged      = errs.NewFailedPreconditionError("role is unchanged")
	ErrConcurrentRoleEdit = errs.NewAbortedError("role changed concurrently")
)

// MaxDisplayNameLength matches the users.display_name database constraint.
const MaxDisplayNameLength = 100

type ProfileUsers interface {
	GetByID(context.Context, uuid.UUID) (*models.User, error)
	UpdateActiveProfile(context.Context, *models.User) error
}

type ProfileService struct{ Users ProfileUsers }

// ProfileInput is the complete editable profile state. The phone number is
// required.
type ProfileInput struct {
	DisplayName string
	Description string
	PhoneNumber string
}

func (s *ProfileService) GetMyProfile(ctx context.Context, actorID uuid.UUID) (models.User, error) {
	if actorID == uuid.Nil {
		return models.User{}, ErrUnauthenticated
	}
	user, err := s.Users.GetByID(ctx, actorID)
	if errors.Is(err, store.ErrNotFound) {
		return models.User{}, ErrUnauthenticated
	}
	if err != nil {
		return models.User{}, fmt.Errorf("get profile: %w", err)
	}
	return *user, nil
}

func (s *ProfileService) UpdateMyProfile(ctx context.Context, actorID uuid.UUID, input ProfileInput) (models.User, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > MaxDisplayNameLength ||
		utf8.RuneCountInString(input.Description) > 500 {
		return models.User{}, ErrInvalidProfile
	}
	phone, ok := normalizedPhoneNumber(input.PhoneNumber)
	if !ok {
		return models.User{}, ErrInvalidProfile
	}
	user, err := s.GetMyProfile(ctx, actorID)
	if err != nil {
		return models.User{}, err
	}
	if user.Role == models.RoleSuspended {
		return models.User{}, ErrPermissionDenied
	}
	user.DisplayName = input.DisplayName
	user.Description = input.Description
	user.PhoneNumber = &phone
	user.UpdatedBy = &actorID
	if err := s.Users.UpdateActiveProfile(ctx, &user); err != nil {
		if errors.Is(err, store.ErrRoleConflict) {
			return models.User{}, ErrPermissionDenied
		}
		if errors.Is(err, store.ErrNotFound) {
			return models.User{}, ErrUnauthenticated
		}
		return models.User{}, fmt.Errorf("update profile: %w", err)
	}
	return s.GetMyProfile(ctx, actorID)
}

// DefaultPhoneRegion is assumed for numbers entered without a country code.
const DefaultPhoneRegion = "SG"

// normalizedPhoneNumber validates a required phone number with libphonenumber
// and returns it in E.164 form, such as +6591234567.
func normalizedPhoneNumber(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	number, err := phonenumbers.Parse(trimmed, DefaultPhoneRegion)
	if err != nil || !phonenumbers.IsValidNumber(number) {
		return "", false
	}
	return phonenumbers.Format(number, phonenumbers.E164), true
}
