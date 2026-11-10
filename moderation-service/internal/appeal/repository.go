package appeal

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/appealdb"
	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/users"
	"github.com/google/uuid"
)

// Repository is the appeal persistence surface. Methods that take q run on
// the active transaction; the others read through the repository's own
// connection.
type Repository interface {
	// Submission

	// AppealByKey returns the appeal an earlier submission with the same key
	// created, or errAppealNotFound.
	AppealByKey(ctx context.Context, q *appealdb.Queries, appellantID, key uuid.UUID) (ExistingAppeal, error)
	// InsertAppeal reports false when a concurrent submission already used the key.
	InsertAppeal(ctx context.Context, q *appealdb.Queries, input AppealInput) (bool, error)
	// InsertDecision returns errDecisionAlreadyAppeal when another unclosed
	// appeal challenges the same decision.
	InsertDecision(ctx context.Context, q *appealdb.Queries, input DecisionInput) error
	ReportFacts(ctx context.Context, q *appealdb.Queries, id uuid.UUID) (ReportFacts, error)
	// ReviewRetainedByReport reports whether reporterID closed a report on the
	// review with no action, keeping it.
	ReviewRetainedByReport(ctx context.Context, q *appealdb.Queries, reviewID, reporterID uuid.UUID) (bool, error)

	// Reads

	GetAppeal(ctx context.Context, id uuid.UUID) (Appeal, error)
	ListResponses(ctx context.Context, appealID uuid.UUID) ([]Response, error)
	ListAppealsInvolving(ctx context.Context, userID uuid.UUID, offset, limit int64) ([]Appeal, error)
	CountAppealsInvolving(ctx context.Context, userID uuid.UUID) (int64, error)
	ListAppeals(ctx context.Context, filter Filter, offset, limit int64) ([]Appeal, error)
	CountAppeals(ctx context.Context, filter Filter) (int64, error)

	// Lifecycle

	// LockAppeal locks the appeal row for the rest of q's transaction.
	LockAppeal(ctx context.Context, q *appealdb.Queries, id uuid.UUID) (LockedAppeal, error)
	LockedDecisions(ctx context.Context, q *appealdb.Queries, appealID uuid.UUID) ([]LockedDecision, error)
	// TransitionAppeal moves the appeal between unclosed statuses and sets the
	// awaited participant (uuid.Nil clears it), or returns errAppealStateChanged.
	TransitionAppeal(ctx context.Context, q *appealdb.Queries, id uuid.UUID, from, to Status, awaited uuid.UUID) error
	// CloseAppeal closes an unclosed appeal, or returns errAppealStateChanged.
	CloseAppeal(ctx context.Context, q *appealdb.Queries, id, closedBy uuid.UUID) error
	// ResolveDecision records an active decision's outcome, or returns
	// errAppealStateChanged.
	ResolveDecision(ctx context.Context, q *appealdb.Queries, appealID uuid.UUID, resolution Resolution) error
	InsertResponse(ctx context.Context, q *appealdb.Queries, input ResponseInput) error
}

// Consequences records how to undo overturned decisions inside the closing
// transaction, for User Service to apply. The composition root binds it to
// the same database transaction as Tx.Queries.
type Consequences interface {
	RemoveWarning(ctx context.Context, removal WarningRemoval) error
	RestoreReview(ctx context.Context, restoration ReviewRestoration) error
	RemoveReview(ctx context.Context, removal ReviewRemoval) error
	Reinstate(ctx context.Context, reinstatement Reinstatement) error
}

// Directory reads users, reviews and warnings from User Service.
type Directory interface {
	People(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]users.Person, error)
	ReviewFacts(ctx context.Context, id uuid.UUID) (users.ReviewFacts, error)
	WarningFacts(ctx context.Context, id uuid.UUID) (users.WarningFacts, error)
}

// WarningRemoval marks a warning removed by an overturned appeal (RP12.1). A
// warning a report issued is named by ReportID instead of WarningID, because
// only User Service knows its ID.
type WarningRemoval struct {
	WarningID uuid.UUID
	ReportID  uuid.UUID
	// UserID is the warned user, set with ReportID.
	UserID   uuid.UUID
	AppealID uuid.UUID
	ActorID  uuid.UUID
	Reason   string
}

// ReviewRestoration restores a removed review (RP12.3).
type ReviewRestoration struct {
	ReviewID uuid.UUID
	ReportID uuid.UUID
	AppealID uuid.UUID
	ActorID  uuid.UUID
	Reason   string
}

// ReviewRemoval removes a review a report kept, when the appeal overturns that
// decision (RP12.4).
type ReviewRemoval struct {
	ReviewID uuid.UUID
	AppealID uuid.UUID
	ActorID  uuid.UUID
	Reason   string
}

// Reinstatement lifts a suspension a report imposed. It is a no-op when the
// user is no longer suspended.
type Reinstatement struct {
	UserID   uuid.UUID
	ReportID uuid.UUID
	AppealID uuid.UUID
	ActorID  uuid.UUID
	Reason   string
}

// Tx is one database transaction seen through the appeal queries and the
// consequences that must commit with them.
type Tx struct {
	Queries      *appealdb.Queries
	Consequences Consequences
	Evidence     EvidenceAttacher
}
