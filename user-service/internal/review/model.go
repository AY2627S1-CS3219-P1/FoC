// Package review implements review submission, listing, removal and review photos.
package review

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Review is one participant's rating of the other for a single request.
type Review struct {
	ID           uuid.UUID
	ReviewerID   uuid.UUID
	ReviewerName string
	RevieweeID   uuid.UUID
	RevieweeName string
	OrderID      uuid.UUID
	Rating       int
	Comment      string
	CreatedAt    time.Time
	DeletedAt    time.Time
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

// newID generates UUIDv7 identifiers until the column default catches up.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid: %w", err)
	}
	return id, nil
}
