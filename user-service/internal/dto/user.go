// Package dto holds JSON response shapes shared across handlers.
package dto

import (
	"time"

	"github.com/google/uuid"

	"user-service/internal/models"
)

// PublicProfile is what any logged-in user may see (NFR-06.1: name,
// picture, description only).
type PublicProfile struct {
	ID                uuid.UUID `json:"id"`
	DisplayName       string    `json:"display_name"`
	Description       string    `json:"description"`
	ProfilePictureKey *string   `json:"profile_picture_key"`
}

// FullProfile is for the user themself and admins (U3.5).
type FullProfile struct {
	PublicProfile
	Email          string          `json:"email"`
	TelegramHandle *string         `json:"telegram_handle"`
	PhoneNumber    *string         `json:"phone_number"`
	Role           models.RoleName `json:"role"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

func Public(u *models.User) PublicProfile {
	return PublicProfile{
		ID:                u.ID,
		DisplayName:       u.DisplayName,
		Description:       u.Description,
		ProfilePictureKey: u.ProfilePictureKey,
	}
}

func Full(u *models.User) FullProfile {
	return FullProfile{
		PublicProfile:  Public(u),
		Email:          u.Email,
		TelegramHandle: u.TelegramHandle,
		PhoneNumber:    u.PhoneNumber,
		Role:           u.Role,
		CreatedAt:      u.CreatedAt.UTC(),
		UpdatedAt:      u.UpdatedAt.UTC(),
	}
}

// Page wraps a paginated list.
type Page[T any] struct {
	Data   []T   `json:"data"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}
