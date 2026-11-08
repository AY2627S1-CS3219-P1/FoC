package review

import (
	"context"
	"time"

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
	// ReviewedOrderIds returns the subset of orderIDs the reviewer has a live
	// review for, so order lists render one CTA per order.
	ListReviewedOrderIds(ctx context.Context, reviewerID uuid.UUID, orderIDs []uuid.UUID) ([]uuid.UUID, error)
	// ReviewForOrder returns the reviewer's live review for one order.
	ReviewForOrder(ctx context.Context, reviewerID, orderID uuid.UUID) (Review, error)
	ListReviewRemovals(ctx context.Context, offset, limit int64) ([]ReviewRemoval, error)
	CountReviewRemovals(ctx context.Context) (int64, error)
	CheckReviewParticipants(ctx context.Context, reviewerID, revieweeID uuid.UUID) (ReviewParticipantExistence, error)
	// ReviewExists reports whether the reviewer already has a review for the
	// order, including removed ones; it mirrors the reviewer/order unique
	// index so the duplicate pre-check and the insert agree.
	ReviewExists(ctx context.Context, reviewerID, orderID uuid.UUID) (bool, error)

	// Review photos. Single-statement operations (staging insert, reads,
	// cleanup claim/delete) run on the repository's own connection; only
	// AttachReviewPhoto takes the transaction-bound queries because it must
	// commit or roll back with the review.
	InsertReviewPhoto(ctx context.Context, input ReviewPhotoInput) error
	GetReviewPhoto(ctx context.Context, ownerID, id uuid.UUID) (ReviewPhoto, error)
	AttachReviewPhoto(
		ctx context.Context,
		q *reviewdb.Queries,
		ownerID, reviewID uuid.UUID,
		ordinal int,
		photo ReviewPhoto,
	) (int64, error)
	ListReviewPhotosForReviews(ctx context.Context, reviewIDs []uuid.UUID) ([]ReviewPhotoAttached, error)
	ClaimOrphanReviewPhotos(ctx context.Context, createdBefore, now time.Time) ([]ClaimedReviewPhoto, error)
	DeleteOrphanReviewPhoto(ctx context.Context, id uuid.UUID) (int64, error)
}

type ReviewParticipantExistence struct {
	ReviewerExists bool
	RevieweeExists bool
}
