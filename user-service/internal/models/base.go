package models

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// BaseModel provides UUID primary keys and the timestamps and soft deletion
// fields shared by persisted models.
type BaseModel struct {
	ID        uuid.UUID `gorm:"primaryKey;default:gen_random_uuid()"`
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt
}
