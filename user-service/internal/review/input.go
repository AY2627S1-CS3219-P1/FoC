package review

import (
	"time"

	"github.com/google/uuid"
)

// Write models: what callers ask the domain to persist. Persisted rows live in
// model.go and caller-facing shapes in view.go.

// ReviewSubmission is the rating and comment a caller submits.
type ReviewSubmission struct {
	Rating  int
	Comment string
}

// ReviewInput carries the fields of a new review row.
type ReviewInput struct {
	ID         uuid.UUID
	ReviewerID uuid.UUID
	RevieweeID uuid.UUID
	OrderID    uuid.UUID
	Rating     int
	Comment    string
}

// ReviewPhotoInput carries the fields of a new staging row.
type ReviewPhotoInput struct {
	ID              uuid.UUID
	OwnerID         uuid.UUID
	ObjectKey       string
	ContentType     string
	SizeBytes       int64
	UploadExpiresAt time.Time
}
