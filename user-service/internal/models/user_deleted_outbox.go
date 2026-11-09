package models

import (
	"time"

	"github.com/google/uuid"
)

// UserDeletedOutbox has no users FK, so the committed fact outlives the account.
// It contains no copied profile, email, credentials or name.
type UserDeletedOutbox struct {
	EventID           uuid.UUID `gorm:"primaryKey"`
	UserID            uuid.UUID
	DeletionRequestID uuid.UUID
	DeletedAt         time.Time
	PayloadVersion    int32
	CreatedAt         time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	AvailableAt       time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	PublishedAt       *time.Time
	LeaseToken        *uuid.UUID
	LeaseUntil        *time.Time
}

func (UserDeletedOutbox) TableName() string { return "user_deleted_outbox" }
