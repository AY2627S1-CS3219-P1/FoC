// Package review implements the review subdomain: participants rate each other
// for a Completed request, and administrators remove (soft-delete) reviews and
// audit it. The whole stack -- domain model, repository interface, transactional
// service logic, sqlc repository and Connect handler -- lives here.
package review

import (
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
)

// Review is one participant's rating of the other for a single request. Reads
// only return live rows, so DeletedAt is always the zero time in practice; the
// field mirrors the soft-delete marker the table carries.
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
}

type ReviewSubmission struct {
	Rating  int
	Comment string
}

// ReviewRemoval is the audit row for a removed review; ReviewID references the
// soft-deleted (retained) review.
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

type ReviewPage struct {
	Reviews []Review
	pagination.PageInfo
}

type RemovalPage struct {
	Removals []ReviewRemoval
	pagination.PageInfo
}

type ReviewInput struct {
	ID         uuid.UUID
	ReviewerID uuid.UUID
	RevieweeID uuid.UUID
	OrderID    uuid.UUID
	Rating     int
	Comment    string
}

// newID generates identifiers in Go because the database column default is
// still gen_random_uuid() until a system-wide uuidv7 migration lands.
func newID() (uuid.UUID, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("generate uuid: %w", err)
	}
	return id, nil
}

var (
	errSelfReview          = errors.New("cannot review yourself")
	errReviewerNotFound    = errors.New("reviewer does not exist")
	errRevieweeNotFound    = errors.New("reviewee does not exist")
	errReviewNotFound      = errors.New("review not found")
	errReviewAlreadyExists = errors.New("review already exists for this participant and request")
	errReviewInvalid       = errors.New("review data is invalid")
)
