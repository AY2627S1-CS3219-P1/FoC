// Package models holds the GORM entities for every table in the user
// service. Tables are created by migrations/, never by AutoMigrate; the tags
// here mirror the SQL so GORM reads/writes the right columns.
package models

import (
	"time"

	"github.com/google/uuid"
)

// User (migrations 000001 + 000003).
type User struct {
	ID                uuid.UUID `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	Email             string    `gorm:"type:citext;not null;uniqueIndex"`
	DisplayName       string    `gorm:"size:50;not null"`
	Description       string    `gorm:"size:500;not null;default:''"`
	TelegramHandle    *string   `gorm:"size:32"`
	PhoneNumber       *string   `gorm:"size:20"`
	ProfilePictureKey *string   // S3 object key; nil => default avatar (U3.1.1)
	Role              RoleName  `gorm:"size:32;not null;default:user"` // FK -> roles.name
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (User) TableName() string { return "users" }

func (u *User) IsSuspended() bool { return u.Role == RoleSuspended }

// IsAdmin is true for admin and super_admin.
func (u *User) IsAdmin() bool { return u.Role.IsAdmin() }

// FavouriteSupplier (000005, U3.4). SupplierID points into the supplier
// service, so there is no FK.
type FavouriteSupplier struct {
	UserID     uuid.UUID `gorm:"type:uuid;primaryKey"`
	SupplierID uuid.UUID `gorm:"type:uuid;primaryKey"`
	CreatedAt  time.Time
}

func (FavouriteSupplier) TableName() string { return "favourite_suppliers" }
