package report

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reportdb"
	"github.com/google/uuid"
)

// Repository is the report persistence surface. Transactional writes take the
// queries bound to the active transaction; reads use the repository's own
// connection.
type Repository interface {
	UserExists(ctx context.Context, id uuid.UUID) (bool, error)
	// LiveReviewAuthor returns the author of a review that has not been removed,
	// or errReviewNotFound.
	LiveReviewAuthor(ctx context.Context, reviewID uuid.UUID) (uuid.UUID, error)
	InsertReport(ctx context.Context, q *reportdb.Queries, input ReportInput) error
	GetReport(ctx context.Context, id uuid.UUID) (Report, error)
	ListResponses(ctx context.Context, reportID uuid.UUID) ([]Response, error)
	ListReportsInvolving(ctx context.Context, userID uuid.UUID, offset, limit int64) ([]Report, error)
	CountReportsInvolving(ctx context.Context, userID uuid.UUID) (int64, error)
	ListReports(ctx context.Context, filter Filter, offset, limit int64) ([]Report, error)
	CountReports(ctx context.Context, filter Filter) (int64, error)

	// LockReport locks the report row for the rest of q's transaction.
	LockReport(ctx context.Context, q *reportdb.Queries, id uuid.UUID) (LockedReport, error)
	// TransitionReport moves the report from one unclosed status to another, or
	// returns errReportStateChanged.
	TransitionReport(ctx context.Context, q *reportdb.Queries, id uuid.UUID, from, to Status) error
	// CloseReport closes an unclosed report, or returns errReportStateChanged.
	CloseReport(ctx context.Context, q *reportdb.Queries, id uuid.UUID, closure Closure) error
	InsertResponse(ctx context.Context, q *reportdb.Queries, input ResponseInput) error
}

// Consequences applies a close outcome inside the closing transaction. The
// composition root binds it to the same database transaction as Tx.Queries.
type Consequences interface {
	RecordWarning(ctx context.Context, warning Warning) error
	Suspend(ctx context.Context, suspension Suspension) error
	RemoveReview(ctx context.Context, actorID, reviewID uuid.UUID, reason string) error
}

// Tx is one database transaction seen through the report queries and the
// consequences that must commit with them.
type Tx struct {
	Queries      *reportdb.Queries
	Consequences Consequences
	Evidence     EvidenceAttacher
}
