package review

import (
	"context"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// Transactional methods use q; list reads and the relay use the repository connection.
type Repository interface {
	CheckReviewParticipants(
		ctx context.Context,
		q *reviewdb.Queries,
		reviewerID, revieweeID uuid.UUID,
	) (ReviewParticipantExistence, error)
	InsertReview(ctx context.Context, q *reviewdb.Queries, input ReviewInput) (InsertedReview, error)
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
		ctx context.Context, reviewerID uuid.UUID, offset, limit int64,
	) ([]UnpublishedReview, error)
	CountUnpublishedReviewsForUser(ctx context.Context, reviewerID uuid.UUID) (int64, error)
	ListReviewRemovals(ctx context.Context, offset, limit int64) ([]ReviewRemoval, error)
	CountReviewRemovals(ctx context.Context) (int64, error)
	// ListReviewOrderStatuses separates live and removed reviews in one snapshot.
	ListReviewOrderStatuses(
		ctx context.Context,
		reviewerID uuid.UUID,
		orderIDs []uuid.UUID,
	) (ReviewOrderStatuses, error)
	// ReviewForOrder returns a live review, or errReviewRemoved without removed content.
	ReviewForOrder(ctx context.Context, reviewerID, orderID uuid.UUID) (Review, error)
	// ReviewExists reports an existing review, including removed ones (mirrors the unique index).
	ReviewExists(ctx context.Context, reviewerID, orderID uuid.UUID) (bool, error)

	// Review photos; only AttachReviewPhoto takes transaction-bound queries.
	InsertReviewPhoto(ctx context.Context, input ReviewPhotoInput) error
	GetAttachedReviewPhoto(ctx context.Context, id uuid.UUID) (AttachedReviewPhoto, error)
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

// Deduplication, verdict and statistics share the consumer transaction.
type ModerationStore interface {
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
