package models

import (
	"time"

	"github.com/google/uuid"
)

// RoleChange is one promote / demote / suspend / reinstate event (U4, U6).
// Append-only. Reason is required when suspending or reinstating.
type RoleChange struct {
	ID        uuid.UUID  `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null"`
	FromRole  RoleName   `gorm:"size:32;not null"`
	ToRole    RoleName   `gorm:"size:32;not null"`
	Reason    *string    `gorm:"size:2000"`
	ActorID   *uuid.UUID `gorm:"type:uuid"` // nil = system
	ReportID  *uuid.UUID `gorm:"type:uuid"`
	CreatedAt time.Time
}

// TableName maps RoleChange to the role_changes table for GORM.
func (RoleChange) TableName() string { return "role_changes" }

type WarningStatus string

const (
	WarningActive  WarningStatus = "active"
	WarningRemoved WarningStatus = "removed"
)

// AccountWarning (U7). Kept when an appeal is Upheld, marked removed when
// Overturned.
type AccountWarning struct {
	ID            uuid.UUID     `gorm:"type:uuid;primaryKey;default:gen_random_uuid()"`
	UserID        uuid.UUID     `gorm:"type:uuid;not null"`
	RequestID     uuid.UUID     `gorm:"type:uuid;not null"`
	ReportID      *uuid.UUID    `gorm:"type:uuid"`
	Reason        string        `gorm:"size:2000;not null"`
	Status        WarningStatus `gorm:"size:16;not null;default:active"`
	SourceEventID *uuid.UUID    `gorm:"type:uuid;uniqueIndex"`
	CreatedAt     time.Time
	RemovedAt     *time.Time
	RemovedReason *string    `gorm:"size:2000"`
	AppealID      *uuid.UUID `gorm:"type:uuid"`
}

// TableName maps AccountWarning to the account_warnings table for GORM.
func (AccountWarning) TableName() string { return "account_warnings" }
