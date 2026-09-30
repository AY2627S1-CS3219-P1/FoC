// Role change history and account warnings.

package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

type RoleChange struct {
	gorm.Model
	Userstamps
	UserID   uint
	FromRole RoleName
	ToRole   RoleName
	Reason   *string
	ReportID *uuid.UUID
}

type WarningStatus string

const (
	WarningActive  WarningStatus = "active"
	WarningRemoved WarningStatus = "removed"
)

type AccountWarning struct {
	gorm.Model
	Userstamps
	UserID        uint
	RequestID     uuid.UUID
	ReportID      *uuid.UUID
	Reason        string
	Status        WarningStatus `gorm:"default:active"`
	SourceEventID *uuid.UUID
	RemovedAt     *time.Time
	RemovedReason *string
	AppealID      *uuid.UUID
}
