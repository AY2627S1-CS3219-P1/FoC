package review

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// Repository is the review persistence surface; writes take transaction-bound queries.
type Repository interface {
	InsertReview(ctx context.Context, q *reviewdb.Queries, input ReviewInput) error
	// SoftDeleteReview removes a live review and returns it for the audit row.
	SoftDeleteReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) (Review, error)
	RecordReviewRemoval(ctx context.Context, q *reviewdb.Queries, removal ReviewRemoval) error
	// RecalculateStats locks the reviewee row, then updates its stats in q's transaction.
	RecalculateStats(ctx context.Context, q *reviewdb.Queries, revieweeID uuid.UUID) error

	ListReviewsForUser(ctx context.Context, userID uuid.UUID, offset, limit int64) ([]Review, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, offset, limit int64) ([]Review, error)
	CountReviewsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountReviewsFromUser(ctx context.Context, userID uuid.UUID) (int64, error)
	// ReviewedOrderIds returns the order ids the reviewer already reviewed.
	ListReviewedOrderIds(ctx context.Context, reviewerID uuid.UUID, orderIDs []uuid.UUID) ([]uuid.UUID, error)
	// ReviewForOrder returns the reviewer's live review for one order.
	ReviewForOrder(ctx context.Context, reviewerID, orderID uuid.UUID) (Review, error)
	ListReviewRemovals(ctx context.Context, offset, limit int64) ([]ReviewRemoval, error)
	CountReviewRemovals(ctx context.Context) (int64, error)
	CheckReviewParticipants(ctx context.Context, reviewerID, revieweeID uuid.UUID) (ReviewParticipantExistence, error)
	// ReviewExists reports an existing review, including removed ones (mirrors the unique index).
	ReviewExists(ctx context.Context, reviewerID, orderID uuid.UUID) (bool, error)

	// Review photos; only AttachReviewPhoto takes transaction-bound queries.
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

// ReviewPhotoAttached is an attached photo as listed for a page of reviews.
type ReviewPhotoAttached struct {
	ID          uuid.UUID
	ReviewID    uuid.UUID
	ObjectKey   string
	ContentType string
	Ordinal     int
}

// ClaimedReviewPhoto awaits object and row deletion by cleanup.
type ClaimedReviewPhoto struct {
	ID        uuid.UUID
	ObjectKey string
}

type ReviewParticipantExistence struct {
	ReviewerExists bool
	RevieweeExists bool
}
