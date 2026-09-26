package models

import (
	"time"

	"github.com/google/uuid"
)

// RoleName is the value stored in users.role (FK to roles.name).
type RoleName string

// Seeded in migration 000003. Add new roles with a migration + a const here.
const (
	RoleSuperAdmin RoleName = "super_admin"
	RoleAdmin      RoleName = "admin"
	RoleUser       RoleName = "user"
	RoleSuspended  RoleName = "suspended"
)

// AllRoles lists every seeded role.
var AllRoles = []RoleName{RoleSuperAdmin, RoleAdmin, RoleUser, RoleSuspended}

// Valid reports whether r is a known role.
func (r RoleName) Valid() bool {
	for _, x := range AllRoles {
		if r == x {
			return true
		}
	}
	return false
}

// IsAdmin is true for admin and super_admin.
func (r RoleName) IsAdmin() bool { return r == RoleAdmin || r == RoleSuperAdmin }

// Role is the lookup table of valid roles. Authorisation rules live in the
// handlers, not in the DB.
type Role struct {
	Name        RoleName `gorm:"size:32;primaryKey"`
	Description string   `gorm:"size:255;not null;default:''"`
	CreatedAt   time.Time
}

// TableName maps Role to the roles table for GORM.
func (Role) TableName() string { return "roles" }

// AdminBootstrap is a singleton row guarding first-admin signup (U4.2).
type AdminBootstrap struct {
	Singleton      bool      `gorm:"primaryKey;default:true"`
	UserID         uuid.UUID `gorm:"type:uuid;not null"`
	BootstrappedAt time.Time `gorm:"not null;autoCreateTime"`
}

// TableName maps AdminBootstrap to the admin_bootstrap table for GORM.
func (AdminBootstrap) TableName() string { return "admin_bootstrap" }
