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

// ModerationStatus is a review's publication status. Only approved reviews are
// published to the reviewee; historical rows were grandfathered as approved.
type ModerationStatus string

const (
	ModerationPending  ModerationStatus = "pending_moderation"
	ModerationApproved ModerationStatus = "approved"
	ModerationRejected ModerationStatus = "rejected"
	ModerationError    ModerationStatus = "moderation_error"
)

// parseModerationStatus converts a stored status string, rejecting values the
// column constraint should never have allowed.
func parseModerationStatus(value string) (ModerationStatus, error) {
	switch status := ModerationStatus(value); status {
	case ModerationPending, ModerationApproved, ModerationRejected, ModerationError:
		return status, nil
	default:
		return "", fmt.Errorf("unknown moderation status %q", value)
	}
}

// UnpublishedReview is the author's view of a review that has not been
// published yet: pending moderation, rejected, or a moderation error.
type UnpublishedReview struct {
	ID                    uuid.UUID
	ReviewerID            uuid.UUID
	ReviewerName          string
	RevieweeID            uuid.UUID
	RevieweeName          string
	OrderID               uuid.UUID
	Rating                int
	Comment               string
	CreatedAt             time.Time
	ModerationStatus      ModerationStatus
	ModerationRequestID   uuid.UUID
	ContentRevision       int
	ModerationReasonCodes []string
	ModeratedAt           time.Time
	AppliedRunGeneration  int32
}

// SubmissionResult reports the committed submission so the caller can recover
// from an ambiguous response without creating a second review.
type SubmissionResult struct {
	ReviewID            uuid.UUID
	ModerationRequestID uuid.UUID
	Status              ModerationStatus
}

// InsertedReview is the persisted state of a freshly submitted review, read
// back from the database so the outbox payload matches the stored row.
type InsertedReview struct {
	CreatedAt        time.Time
	ModerationStatus ModerationStatus
	ContentRevision  int
}

// UnpublishedFilter narrows the unpublished list. Nil fields mean no filter.
type UnpublishedFilter struct {
	Status  *ModerationStatus
	OrderID *uuid.UUID
}

// ModerationTarget is the review state the completion handler reads after
// locking the row.
type ModerationTarget struct {
	ID                   uuid.UUID
	ReviewerID           uuid.UUID
	RevieweeID           uuid.UUID
	ModerationStatus     ModerationStatus
	ModerationRequestID  uuid.UUID
	ContentRevision      int
	AppliedRunGeneration int32
	Deleted              bool
}

// ModerationCompletion is a validated completion event: the submission identity
// copied from the event, and the verdict to apply to the matching review.
type ModerationCompletion struct {
	// Identity from the submission; the handler checks it against the saved
	// review before applying anything.
	ReviewerID          uuid.UUID
	ModerationRequestID uuid.UUID
	ContentRevision     int

	// The verdict. Status is one of approved, rejected, moderation_error.
	Status          ModerationStatus
	RunGeneration   int32
	PolicyVersion   string
	ConfigVersionID string
	ReasonCodes     []string
	ModeratedAt     time.Time
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

type UnpublishedReviewPage struct {
	Reviews []UnpublishedReview
	pagination.PageInfo
}

type RemovalPage struct {
	Removals []ReviewRemoval
	pagination.PageInfo
}

type ReviewInput struct {
	ID                  uuid.UUID
	ReviewerID          uuid.UUID
	RevieweeID          uuid.UUID
	OrderID             uuid.UUID
	Rating              int
	Comment             string
	ModerationRequestID uuid.UUID
}

// SubmittedOutboxRow is the outbox row appended with the review: the complete
// ReviewSubmitted payload, published by the relay after the commit.
type SubmittedOutboxRow struct {
	EventID        uuid.UUID
	ReviewID       uuid.UUID
	PayloadVersion int32
	Payload        []byte
}

// SubmittedFact is a claimed outbox row: the payload plus the review id used as
// the Kafka key and the lease token guarding publication.
type SubmittedFact struct {
	EventID        uuid.UUID
	ReviewID       uuid.UUID
	PayloadVersion int32
	Payload        []byte
	LeaseToken     uuid.UUID
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
