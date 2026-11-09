package models

import (
	"time"

	"github.com/google/uuid"
)

const (
	ProfilePicturePending  = "pending"
	ProfilePictureActive   = "active"
	ProfilePictureDeleting = "deleting"
)

// ProfilePicture is one picture row; object_key is storage metadata only.
type ProfilePicture struct {
	ID            uuid.UUID `gorm:"primaryKey"`
	UserID        uuid.UUID
	ObjectKey     string
	ContentType   string
	SizeBytes     int64
	State         string
	CreatedAt     time.Time
	DeleteRetryAt *time.Time
}
