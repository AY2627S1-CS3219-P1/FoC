// Roles and the first-admin bootstrap row.

package models

import "time"

type RoleName string

const (
	RoleSuperAdmin RoleName = "super_admin"
	RoleAdmin      RoleName = "admin"
	RoleUser       RoleName = "user"
	RoleSuspended  RoleName = "suspended"
)

var AllRoles = []RoleName{RoleSuperAdmin, RoleAdmin, RoleUser, RoleSuspended}

func (r RoleName) Valid() bool {
	for _, x := range AllRoles {
		if r == x {
			return true
		}
	}
	return false
}

func (r RoleName) IsAdmin() bool { return r == RoleAdmin || r == RoleSuperAdmin }

type Role struct {
	Name        RoleName `gorm:"primaryKey"`
	Description string
	CreatedAt   time.Time
}

type AdminBootstrap struct {
	Singleton      bool `gorm:"primaryKey;default:true"`
	UserID         uint
	BootstrappedAt time.Time `gorm:"autoCreateTime"`
}

func (AdminBootstrap) TableName() string { return "admin_bootstrap" }
