package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/models"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/store"
)

var (
	ErrUnauthenticated    = errors.New("user is not authenticated")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrInvalidProfile     = errors.New("invalid profile")
	ErrUserNotFound       = errors.New("user not found")
	ErrInvalidRoleChange  = errors.New("invalid role change")
	ErrRoleUnchanged      = errors.New("role is unchanged")
	ErrConcurrentRoleEdit = errors.New("role changed concurrently")
)

type ProfileUsers interface {
	GetByID(context.Context, uint) (*models.User, error)
	UpdateActiveProfile(context.Context, *models.User) error
}

type ProfileService struct{ Users ProfileUsers }

// ProfileInput is the complete editable profile state. Empty contact strings
// clear the corresponding nullable database columns.
type ProfileInput struct {
	DisplayName    string
	Description    string
	TelegramHandle *string
	PhoneNumber    *string
}

func (s *ProfileService) GetMyProfile(ctx context.Context, actorID uint) (models.User, error) {
	if actorID == 0 {
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

func (s *ProfileService) UpdateMyProfile(ctx context.Context, actorID uint, input ProfileInput) (models.User, error) {
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" || utf8.RuneCountInString(input.DisplayName) > 100 ||
		utf8.RuneCountInString(input.Description) > 500 {
		return models.User{}, ErrInvalidProfile
	}
	var ok bool
	input.TelegramHandle, ok = normalizedContact(input.TelegramHandle, 32)
	if !ok {
		return models.User{}, ErrInvalidProfile
	}
	if input.TelegramHandle != nil && !telegramHandlePattern.MatchString(*input.TelegramHandle) {
		return models.User{}, ErrInvalidProfile
	}
	input.PhoneNumber, ok = normalizedContact(input.PhoneNumber, 20)
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
	user.TelegramHandle = input.TelegramHandle
	user.PhoneNumber = input.PhoneNumber
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

var telegramHandlePattern = regexp.MustCompile(`^[A-Za-z0-9_]{5,32}$`)

func normalizedContact(value *string, max int) (*string, bool) {
	if value == nil {
		return nil, true
	}
	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil, true
	}
	if utf8.RuneCountInString(trimmed) > max {
		return nil, false
	}
	return &trimmed, true
}
