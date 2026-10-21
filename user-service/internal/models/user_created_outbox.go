package models

import (
	"time"

	"github.com/google/uuid"
)

// UserCreatedOutbox stores a registration snapshot until Kafka acknowledges it.
type UserCreatedOutbox struct {
	EventID        uuid.UUID `gorm:"primaryKey"`
	UserID         uuid.UUID `gorm:"uniqueIndex"`
	Email          string
	DisplayName    string
	UserCreatedAt  time.Time
	PayloadVersion int32
	CreatedAt      time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	AvailableAt    time.Time `gorm:"default:CURRENT_TIMESTAMP"`
	PublishedAt    *time.Time
	LeaseToken     *uuid.UUID
	LeaseUntil     *time.Time
}

func (UserCreatedOutbox) TableName() string { return "user_created_outbox" }
