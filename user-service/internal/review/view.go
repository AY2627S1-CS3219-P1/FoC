package review

import (
	"time"

	"github.com/google/uuid"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
)

// Read models returned to callers. Persisted state lives in model.go.

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
