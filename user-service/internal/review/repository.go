package review

import (
	"context"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// Repository is the review persistence surface. Transactional write operations
// take the review queries bound to the active transaction so every statement
// they run commits or rolls back with the caller's transaction. Reads use the
// repository's own connection.
type Repository interface {
	InsertReview(ctx context.Context, q *reviewdb.Queries, input ReviewInput) error
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
	ListReviewRemovals(ctx context.Context, offset, limit int64) ([]ReviewRemoval, error)
	CountReviewRemovals(ctx context.Context) (int64, error)
	// CheckReviewParticipants checks whether both participants exist. For live
	// eligibility during submission, use LockAndCheckParticipants instead.
	CheckReviewParticipants(ctx context.Context, reviewerID, revieweeID uuid.UUID) (ReviewParticipantExistence, error)
	// LockAndCheckParticipants locks both participants in a deterministic order
	// and returns their live status. A participant is live if the row exists,
	// is not soft-deleted (deleted_at IS NULL) and is not permanently deleting.
	LockAndCheckParticipants(
		ctx context.Context,
		q *reviewdb.Queries,
		reviewerID, revieweeID uuid.UUID,
	) (ParticipantStatus, error)
}

type ReviewParticipantExistence struct {
	ReviewerExists bool
	RevieweeExists bool
}

type ParticipantStatus struct {
	ReviewerExists bool
	RevieweeExists bool
	ReviewerLive   bool // exists AND not soft-deleted AND not deleting
	RevieweeLive   bool // exists AND not soft-deleted AND not deleting
	ReviewerID     uuid.UUID
	RevieweeID     uuid.UUID
}
