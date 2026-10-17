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
	FindReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) (Review, error)
	DeleteReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) error
	InsertReviewRemoval(ctx context.Context, q *reviewdb.Queries, removal ReviewRemoval) error
	// RecalculateStats locks the reviewee row, reads COUNT/SUM in a separate
	// statement and updates the user's stats, all within q's transaction.
	RecalculateStats(ctx context.Context, q *reviewdb.Queries, revieweeID uuid.UUID) error

	ListReviewsForUser(ctx context.Context, userID uuid.UUID, offset, limit int) ([]Review, error)
	ListReviewsFromUser(ctx context.Context, userID uuid.UUID, offset, limit int) ([]Review, error)
	CountReviewsForUser(ctx context.Context, userID uuid.UUID) (int64, error)
	CountReviewsFromUser(ctx context.Context, userID uuid.UUID) (int64, error)
	ListReviewRemovals(ctx context.Context) ([]ReviewRemoval, error)
	UserExists(ctx context.Context, id uuid.UUID) (bool, error)
}
