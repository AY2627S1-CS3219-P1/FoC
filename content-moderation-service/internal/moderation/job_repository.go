package moderation

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// jobOperations is the durable transition port consumed by the worker application.
// Implementations own transactions, budgets and fencing; network evaluation is external.
type jobOperations interface {
	Claim(context.Context, string, time.Duration) (ClaimOutcome, *ClaimedJob, error)
	Heartbeat(context.Context, uuid.UUID, uuid.UUID, time.Time, time.Time) (bool, error)
	Release(context.Context, uuid.UUID, uuid.UUID) (bool, error)
	Complete(context.Context, ClaimedJob, ReviewAssessment) error
	FinalizeFailed(context.Context, ClaimedJob, error) error
}

type ClaimOutcome int

const (
	ClaimNone ClaimOutcome = iota
	ClaimNoConfiguration
	ClaimReady
	ClaimExhausted
)

// ClaimedJob is the immutable snapshot bound to a fenced attempt.
type ClaimedJob struct {
	Job           JobSnapshot
	ClaimToken    uuid.UUID
	AttemptNumber int32
	Config        ConfigurationSnapshot
}

type JobSnapshot struct {
	ID                  uuid.UUID
	ModerationRequestID uuid.UUID
	SourceEventID       uuid.UUID
	ReviewID            uuid.UUID
	ReviewerID          uuid.UUID
	ContentRevision     int32
	Comment             string
	ConfigVersionID     *string
	PolicyVersion       *string
	RunGeneration       int32
}

type ConfigurationSnapshot struct {
	SystemMessage       string
	VersionID           uuid.UUID
	ModelID             string
	Parameters          []byte
	OutputMode          string
	PolicyVersion       string
	ProviderFingerprint string
}
