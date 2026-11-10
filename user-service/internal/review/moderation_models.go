package review

import (
	"fmt"
	"github.com/google/uuid"
	"time"
)

// Public reads and ratings include only approved reviews.
type ModerationStatus string

const (
	ModerationPending  ModerationStatus = "pending_moderation"
	ModerationApproved ModerationStatus = "approved"
	ModerationRejected ModerationStatus = "rejected"
	ModerationError    ModerationStatus = "moderation_error"
)

func parseModerationStatus(value string) (ModerationStatus, error) {
	switch status := ModerationStatus(value); status {
	case ModerationPending, ModerationApproved, ModerationRejected, ModerationError:
		return status, nil
	default:
		return "", fmt.Errorf("unknown moderation status %q", value)
	}
}

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

type ModerationCompletion struct {
	ReviewerID          uuid.UUID
	ModerationRequestID uuid.UUID
	ContentRevision     int

	Status        ModerationStatus
	RunGeneration int32
	PolicyVersion string
	ReasonCodes   []string
	ModeratedAt   time.Time
}

type SubmittedOutboxRow struct {
	EventID        uuid.UUID
	ReviewID       uuid.UUID
	PayloadVersion int32
	Payload        []byte
}

type SubmittedFact struct {
	EventID        uuid.UUID
	ReviewID       uuid.UUID
	PayloadVersion int32
	Payload        []byte
	LeaseToken     uuid.UUID
}
