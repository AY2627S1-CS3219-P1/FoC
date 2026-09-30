// Package models holds the GORM entities for the user service. The schema is
// owned by migrations/; tags here only change how GORM reads and writes.
package models

import (
	"time"

	"github.com/google/uuid"
)

type Userstamps struct {
	CreatedBy *uuid.UUID
	UpdatedBy *uuid.UUID
}

type User struct {
	BaseModel
	Userstamps
	Email             string
	DisplayName       string
	Description       string
	TelegramHandle    *string
	PhoneNumber       *string
	ProfilePictureKey *string
	Role              RoleName `gorm:"default:user"`
}

func (u *User) IsSuspended() bool { return u.Role == RoleSuspended }

func (u *User) IsAdmin() bool { return u.Role.IsAdmin() }

type FavouriteSupplier struct {
	UserID     uuid.UUID `gorm:"primaryKey"`
	SupplierID uuid.UUID `gorm:"primaryKey"`
	CreatedAt  time.Time
}
