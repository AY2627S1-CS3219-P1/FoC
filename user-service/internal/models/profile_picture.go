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

// ProfilePicture is one profile picture row. The row is the application-level
// reference; object_key is only the storage attribute for these bytes.
type ProfilePicture struct {
	ID          uuid.UUID `gorm:"primaryKey"`
	UserID      uuid.UUID
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	State       string
	CreatedAt   time.Time
}
