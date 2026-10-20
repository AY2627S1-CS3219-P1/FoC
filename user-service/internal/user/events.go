package user

import (
	"time"

	"github.com/google/uuid"
)

// CreatedEvent is the immutable User Service fact for a successful registration.
// It is independent of the Kafka wire message and database representation.
type CreatedEvent struct {
	EventID     uuid.UUID
	UserID      uuid.UUID
	Email       string
	DisplayName string
	CreatedAt   time.Time
}
