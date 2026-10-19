package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresRepository struct {
	queries *reviewdb.Queries
}

func NewPostgresRepository(queries *reviewdb.Queries) *PostgresRepository {
	return &PostgresRepository{queries: queries}
}

var _ Repository = (*PostgresRepository)(nil)

// Transactional write operations receive the review queries bound to the active
// transaction.
func (r *PostgresRepository) InsertReview(ctx context.Context, q *reviewdb.Queries, input ReviewInput) error {
	err := q.InsertReview(ctx, reviewdb.InsertReviewParams{
		ID:         input.ID,
		ReviewerID: input.ReviewerID,
		RevieweeID: input.RevieweeID,
		OrderID:    input.OrderID,
		Rating:     int32(input.Rating),
		Comment:    input.Comment,
	})
	return mapReviewError("insert review", err)
}

// FindReviewLocked returns the live review under a row lock (see the Repository
// contract). The snapshot comes from the locked row itself, so no relationship
// subquery is involved.
func (r *PostgresRepository) FindReviewLocked(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) (Review, error) {
	row, err := q.FindReviewLocked(ctx, id)
	if err != nil {
		return Review{}, mapReviewError("find review", err)
	}
	return toReview(row), nil
}

// SoftDeleteReview marks the review removed inside q's transaction. The row is
// retained so the removal audit can keep referencing it.
func (r *PostgresRepository) SoftDeleteReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) error {
	return mapReviewError("soft delete review", q.SoftDeleteReview(ctx, id))
}

func (r *PostgresRepository) InsertReviewRemoval(ctx context.Context, q *reviewdb.Queries, removal ReviewRemoval) error {
	return mapReviewError("insert review removal", q.InsertReviewRemoval(ctx, reviewdb.InsertReviewRemovalParams{
		ID:         removal.ID,
		ReviewID:   removal.ReviewID,
		ReviewerID: removal.ReviewerID,
		RevieweeID: removal.RevieweeID,
		OrderID:    removal.OrderID,
		Reason:     removal.Reason,
		CreatedBy:  removal.CreatedBy,
	}))
}

// RecalculateStats recomputes the reviewee's review_count and avg_rating from
// their remaining reviews. It locks the users row first, then reads the
// aggregate in a separate statement (see LANDMINES/locked-row-relationship-
// snapshots.md), then writes the stats -- all within q's transaction.
func (r *PostgresRepository) RecalculateStats(ctx context.Context, q *reviewdb.Queries, revieweeID uuid.UUID) error {
	if err := q.LockReviewee(ctx, revieweeID); err != nil {
		return fmt.Errorf("lock reviewee: %w", err)
	}
	stats, err := q.RevieweeStatistics(ctx, revieweeID)
	if err != nil {
		return fmt.Errorf("read review statistics: %w", err)
	}
	avg := 0.0
	if stats.ReviewCount > 0 {
		avg = float64(stats.SumRating) / float64(stats.ReviewCount)
	}
	if err := q.UpdateRevieweeStats(ctx, reviewdb.UpdateRevieweeStatsParams{
		Count:      int32(stats.ReviewCount),
		AvgRating:  avg,
		RevieweeID: revieweeID,
	}); err != nil {
		return fmt.Errorf("update review statistics: %w", err)
	}
	return nil
}

// Reads use the repository's own connection.
func (r *PostgresRepository) ListReviewsForUser(ctx context.Context, userID uuid.UUID, offset, limit int) ([]Review, error) {
	rows, err := r.queries.ListReviewsForUser(ctx, reviewdb.ListReviewsForUserParams{
		UserID: userID, PageOffset: int32(offset), PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list reviews received: %w", err)
	}
	out := make([]Review, len(rows))
	for i, row := range rows {
		out[i] = toReviewForUser(row)
	}
	return out, nil
}

func (r *PostgresRepository) ListReviewsFromUser(ctx context.Context, userID uuid.UUID, offset, limit int) ([]Review, error) {
	rows, err := r.queries.ListReviewsFromUser(ctx, reviewdb.ListReviewsFromUserParams{
		UserID: userID, PageOffset: int32(offset), PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list reviews given: %w", err)
	}
	out := make([]Review, len(rows))
	for i, row := range rows {
		out[i] = toReviewFromUser(row)
	}
	return out, nil
}

func (r *PostgresRepository) CountReviewsForUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	count, err := r.queries.CountReviewsForUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count reviews received: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) CountReviewsFromUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	count, err := r.queries.CountReviewsFromUser(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("count reviews given: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) ListReviewRemovals(ctx context.Context, offset, limit int) ([]ReviewRemoval, error) {
	rows, err := r.queries.ListReviewRemovals(ctx, reviewdb.ListReviewRemovalsParams{
		PageOffset: int32(offset), PageLimit: int32(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("list removals: %w", err)
	}
	out := make([]ReviewRemoval, len(rows))
	for i, row := range rows {
		out[i] = toReviewRemovalListed(row)
	}
	return out, nil
}

func (r *PostgresRepository) CountReviewRemovals(ctx context.Context) (int64, error) {
	count, err := r.queries.CountReviewRemovals(ctx)
	if err != nil {
		return 0, fmt.Errorf("count removals: %w", err)
	}
	return count, nil
}

func (r *PostgresRepository) UserExists(ctx context.Context, id uuid.UUID) (bool, error) {
	exists, err := r.queries.UserExists(ctx, id)
	if err != nil {
		return false, fmt.Errorf("check user exists: %w", err)
	}
	return exists, nil
}

func toReview(row reviewdb.Review) Review {
	return Review{
		ID:         row.ID,
		ReviewerID: row.ReviewerID,
		RevieweeID: row.RevieweeID,
		OrderID:    row.OrderID,
		Rating:     int(row.Rating),
		Comment:    row.Comment,
		CreatedAt:  row.CreatedAt,
		DeletedAt:  row.DeletedAt.Time,
	}
}

// The list queries join users for display names, so each carries its own row
// type alongside the plain review columns.
func toReviewForUser(row reviewdb.ListReviewsForUserRow) Review {
	return Review{
		ID:           row.ID,
		ReviewerID:   row.ReviewerID,
		ReviewerName: row.ReviewerName,
		RevieweeID:   row.RevieweeID,
		RevieweeName: row.RevieweeName,
		OrderID:      row.OrderID,
		Rating:       int(row.Rating),
		Comment:      row.Comment,
		CreatedAt:    row.CreatedAt,
		DeletedAt:    row.DeletedAt.Time,
	}
}

func toReviewFromUser(row reviewdb.ListReviewsFromUserRow) Review {
	return Review{
		ID:           row.ID,
		ReviewerID:   row.ReviewerID,
		ReviewerName: row.ReviewerName,
		RevieweeID:   row.RevieweeID,
		RevieweeName: row.RevieweeName,
		OrderID:      row.OrderID,
		Rating:       int(row.Rating),
		Comment:      row.Comment,
		CreatedAt:    row.CreatedAt,
		DeletedAt:    row.DeletedAt.Time,
	}
}

// The removals query joins users for the creator's display name, so it returns
// its own row type rather than the table model.
func toReviewRemovalListed(row reviewdb.ListReviewRemovalsRow) ReviewRemoval {
	return ReviewRemoval{
		ID:            row.ID,
		ReviewID:      row.ReviewID,
		ReviewerID:    row.ReviewerID,
		RevieweeID:    row.RevieweeID,
		OrderID:       row.OrderID,
		Reason:        row.Reason,
		CreatedAt:     row.CreatedAt,
		CreatedBy:     row.CreatedBy,
		CreatedByName: row.CreatedByName,
	}
}

// mapReviewError turns database failures into domain sentinels so the service
// can map them to client codes without the repository touching the api layer.
func mapReviewError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errReviewNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			// Unreachable while users are never deleted. When user deletion
			// lands, a violation on an insert means a missing reviewer/reviewee,
			// not a missing review — distinguish by pgErr.ConstraintName then.
			return errReviewNotFound
		case "23505":
			return errReviewAlreadyExists
		case "23514", "23P01":
			return errReviewInvalid
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
