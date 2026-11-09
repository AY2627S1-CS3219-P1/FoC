package review

import (
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
)

// Data crossing the service boundary in either direction; persisted rows are
// in models.go.

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

// ReviewPhotoView is an attached photo with a freshly minted download URL.
type ReviewPhotoView struct {
	ID          uuid.UUID
	URL         string
	ContentType string
	ExpiresAt   time.Time
}

type ReviewPage struct {
	Reviews []Review
	pagination.PageInfo
}

type RemovalPage struct {
	Removals []ReviewRemoval
	pagination.PageInfo
}

// ReviewOrderStatuses distinguishes fetchable reviews from orders blocked by moderation.
type ReviewOrderStatuses struct {
	ReviewedOrderIDs []uuid.UUID
	RemovedOrderIDs  []uuid.UUID
}
