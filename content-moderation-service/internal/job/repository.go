package job

import (
	"context"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/moderation"
	"time"

	"github.com/google/uuid"
)

// jobOperations is the durable transition port consumed by the worker application.
// Implementations own transactions, budgets and fencing; network evaluation is external.
type jobOperations interface {
	Claim(context.Context, string, time.Duration) (ClaimOutcome, *ClaimedJob, error)
	Heartbeat(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time) (bool, error)
	Release(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	Complete(context.Context, ClaimedJob, moderation.ReviewAssessment) error
	FinalizeFailed(context.Context, ClaimedJob, error) error
}

type ClaimOutcome int

const (
	ClaimNone ClaimOutcome = iota
	ClaimReady
	ClaimExhausted
)

// ClaimedJob is the immutable snapshot bound to a fenced attempt.
type ClaimedJob struct {
	Job           JobSnapshot
	ClaimToken    uuid.UUID
	AttemptNumber int32
}

type JobSnapshot struct {
	ID                  uuid.UUID
	ModerationRequestID uuid.UUID
	SourceEventID       uuid.UUID
	ReviewID            uuid.UUID
	ReviewerID          uuid.UUID
	ContentRevision     int32
	Comment             string
	PolicyVersion       *string
	RunGeneration       int32
}
