package review

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Review is one participant's rating of the other for a single request.
type Review struct {
	ModerationStatus ModerationStatus
	ID               uuid.UUID
	ReviewerID       uuid.UUID
	ReviewerName     string
	RevieweeID       uuid.UUID
	RevieweeName     string
	OrderID          uuid.UUID
	Rating           int
	Comment          string
	CreatedAt        time.Time
	DeletedAt        time.Time
	// Photos carries freshly minted download URLs; empty without photos or storage.
	Photos []ReviewPhotoView
}

// ReviewPhoto is a staged or attached row; OwnerID is null after account deletion.
type ReviewPhoto struct {
	ID              uuid.UUID
	OwnerID         uuid.NullUUID
	ObjectKey       string
	ContentType     string
	SizeBytes       int64
	State           string
	ReviewID        uuid.UUID
	Ordinal         int
	CreatedAt       time.Time
	UploadExpiresAt time.Time
}

// ReviewPhotoAttached is an attached photo as listed for a page of reviews.
type ReviewPhotoAttached struct {
	ID          uuid.UUID
	ReviewID    uuid.UUID
	ObjectKey   string
	ContentType string
	Ordinal     int
}

// ClaimedReviewPhoto awaits object and row deletion by cleanup.
type ClaimedReviewPhoto struct {
	ID        uuid.UUID
	ObjectKey string
}

// ReviewRemoval is the audit row for a removed review.
type ReviewRemoval struct {
	ID            uuid.UUID
	ReviewID      uuid.UUID
	ReviewerID    uuid.UUID
	RevieweeID    uuid.UUID
	OrderID       uuid.UUID
	Reason        string
	CreatedAt     time.Time
	CreatedBy     uuid.UUID
	CreatedByName string
}

// ReviewRestoration audits a restored review, answering one removal. AppealID
// is set when an overturned appeal restored it.
type ReviewRestoration struct {
	ID        uuid.UUID
	RemovalID uuid.UUID
	ReviewID  uuid.UUID
	AppealID  uuid.UUID
	Reason    string
	CreatedBy uuid.UUID
}

// newID generates UUIDv7 identifiers until the column default catches up.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid: %w", err)
	}
	return id, nil
}
