package review

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// Repository is the review persistence surface. Transactional write operations
// take the review queries bound to the active transaction so every statement
// they run commits or rolls back with the caller's transaction. Reads and the
// outbox relay use the repository's own connection.
type Repository interface {
	// CheckReviewParticipants verifies both users exist within the caller's
	// transaction, so the checks and the insert see one consistent snapshot.
	CheckReviewParticipants(
		ctx context.Context,
		q *reviewdb.Queries,
		reviewerID, revieweeID uuid.UUID,
	) (ReviewParticipantExistence, error)
	// InsertReview stores the pending review and returns its persisted state.
	InsertReview(ctx context.Context, q *reviewdb.Queries, input ReviewInput) (InsertedReview, error)
	// AppendSubmittedOutbox queues the ReviewSubmitted event in the same
	// transaction as the review row.
	AppendSubmittedOutbox(ctx context.Context, q *reviewdb.Queries, row SubmittedOutboxRow) error
	// SoftDeleteReview removes a live review and returns its data for the removal
	// audit. The row remains so the audit can reference it.
	SoftDeleteReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) (Review, error)
	RecordReviewRemoval(ctx context.Context, q *reviewdb.Queries, removal ReviewRemoval) error
	// RecalculateStats locks the reviewee row, reads COUNT/SUM in a separate
	// statement and updates the user's stats, all within q's transaction.
	RecalculateStats(ctx context.Context, q *reviewdb.Queries, revieweeID uuid.UUID) error

	ListReviewsForUser(ctx context.Context, userID uuid.UUID, offset, limit int64) ([]Review, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, offset, limit int64) ([]Review, error)
	CountReviewsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountReviewsFromUser(ctx context.Context, userID uuid.UUID) (int64, error)
	ListUnpublishedReviewsForUser(
		ctx context.Context, reviewerID uuid.UUID, filter UnpublishedFilter, offset, limit int64,
	) ([]UnpublishedReview, error)
	CountUnpublishedReviewsForUser(ctx context.Context, reviewerID uuid.UUID, filter UnpublishedFilter) (int64, error)
	ListReviewRemovals(ctx context.Context, offset, limit int64) ([]ReviewRemoval, error)
	CountReviewRemovals(ctx context.Context) (int64, error)
}

// ModerationStore is the completion consumer's persistence surface. Every
// method runs within the consumer's transaction (q), so the dedup record, the
// outcome and the stats recalculation commit or roll back together.
type ModerationStore interface {
	// RecordModerationProcessedEvent inserts the dedup record; zero rows means
	// the event was already processed.
	RecordModerationProcessedEvent(ctx context.Context, q *reviewdb.Queries, eventID uuid.UUID) (int64, error)
	// LockReviewForModeration locks the review row; GetReviewForModeration must
	// be called afterwards in a separate statement (see LANDMINES).
	LockReviewForModeration(ctx context.Context, q *reviewdb.Queries, reviewID uuid.UUID) error
	GetReviewForModeration(ctx context.Context, q *reviewdb.Queries, reviewID uuid.UUID) (ModerationTarget, error)
	ApplyModerationOutcome(
		ctx context.Context,
		q *reviewdb.Queries,
		reviewID uuid.UUID,
		completion ModerationCompletion,
	) error
	RecalculateStats(ctx context.Context, q *reviewdb.Queries, revieweeID uuid.UUID) error
}

type ReviewParticipantExistence struct {
	ReviewerExists bool
	RevieweeExists bool
}
