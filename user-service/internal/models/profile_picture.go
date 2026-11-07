package models

import (
	"time"

	"github.com/google/uuid"
)

// ProfilePictureUpload is one pending direct-to-storage grant. ObjectKey is the
// durable object identity; signed URLs are never persisted.
type ProfilePictureUpload struct {
	ID          uuid.UUID `gorm:"primaryKey"`
	UserID      uuid.UUID
	ObjectKey   string
	ContentType string
	SizeBytes   int64
	Deleting    bool
	CreatedAt   time.Time
}
