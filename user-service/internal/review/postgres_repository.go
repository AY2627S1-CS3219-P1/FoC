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

func (r *PostgresRepository) SoftDeleteReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) (Review, error) {
	row, err := q.SoftDeleteReview(ctx, id)
	if err != nil {
		return Review{}, mapReviewError("soft delete review", err)
	}
	return toReview(row), nil
}

func (r *PostgresRepository) RecordReviewRemoval(
	ctx context.Context,
	q *reviewdb.Queries,
	removal ReviewRemoval,
) error {
	return mapReviewError("record review removal", q.RecordReviewRemoval(ctx, reviewdb.RecordReviewRemovalParams{
		ID:         removal.ID,
		ReviewID:   removal.ReviewID,
		ReviewerID: removal.ReviewerID,
		RevieweeID: removal.RevieweeID,
		OrderID:    removal.OrderID,
		Reason:     removal.Reason,
		CreatedBy:  removal.CreatedBy,
	}))
}

// The lock-then-read-then-write order matters; see the LockReviewee query for
// why, and LANDMINES/locked-row-relationship-snapshots.md.
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
		Count:      stats.ReviewCount,
		AvgRating:  avg,
		RevieweeID: revieweeID,
	}); err != nil {
		return fmt.Errorf("update review statistics: %w", err)
	}
	return nil
}

func (r *PostgresRepository) ListReviewsForUser(
	ctx context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]Review, error) {
	rows, err := r.queries.ListReviewsForUser(ctx, reviewdb.ListReviewsForUserParams{
		UserID:     userID,
		PageOffset: offset,
		PageLimit:  limit,
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

func (r *PostgresRepository) ListReviewsFromUser(
	ctx context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]Review, error) {
	rows, err := r.queries.ListReviewsFromUser(ctx, reviewdb.ListReviewsFromUserParams{
		UserID:     userID,
		PageOffset: offset,
		PageLimit:  limit,
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

func (r *PostgresRepository) ListReviewRemovals(ctx context.Context, offset, limit int64) ([]ReviewRemoval, error) {
	rows, err := r.queries.ListReviewRemovals(ctx, reviewdb.ListReviewRemovalsParams{
		PageOffset: offset,
		PageLimit:  limit,
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

func (r *PostgresRepository) CheckReviewParticipants(
	ctx context.Context,
	reviewerID, revieweeID uuid.UUID,
) (ReviewParticipantExistence, error) {
	exists, err := r.queries.ReviewParticipantsExist(ctx, reviewdb.ReviewParticipantsExistParams{
		ReviewerID: reviewerID,
		RevieweeID: revieweeID,
	})
	if err != nil {
		return ReviewParticipantExistence{}, fmt.Errorf("check review participants exist: %w", err)
	}
	return ReviewParticipantExistence{
		ReviewerExists: exists.ReviewerExists,
		RevieweeExists: exists.RevieweeExists,
	}, nil
}

// LockAndCheckParticipants locks both participants in a deterministic id order
// and returns their live status. A participant is live if the row exists,
// is not soft-deleted (deleted_at IS NULL) and is not permanently deleting.
func (r *PostgresRepository) LockAndCheckParticipants(
	ctx context.Context,
	q *reviewdb.Queries,
	reviewerID, revieweeID uuid.UUID,
) (ParticipantStatus, error) {
	rows, err := q.LockSubmissionParticipants(ctx, reviewdb.LockSubmissionParticipantsParams{
		A: reviewerID,
		B: revieweeID,
	})
	if err != nil {
		return ParticipantStatus{}, fmt.Errorf("lock submission participants: %w", err)
	}

	status := ParticipantStatus{
		ReviewerID: reviewerID,
		RevieweeID: revieweeID,
	}

	for _, row := range rows {
		if row.ID == reviewerID {
			status.ReviewerExists = true
			status.ReviewerLive = !row.DeletedAt.Valid && !row.IsDeleting
		}
		if row.ID == revieweeID {
			status.RevieweeExists = true
			status.RevieweeLive = !row.DeletedAt.Valid && !row.IsDeleting
		}
	}

	return status, nil
}

func toReview(row reviewdb.Review) Review {
	return Review{
		ID:         row.ID,
		ReviewerID: row.ReviewerID.UUID,
		RevieweeID: row.RevieweeID.UUID,
		OrderID:    row.OrderID,
		Rating:     int(row.Rating),
		Comment:    row.Comment,
		CreatedAt:  row.CreatedAt,
		DeletedAt:  row.DeletedAt.Time,
	}
}

func toReviewForUser(row reviewdb.ListReviewsForUserRow) Review {
	return Review{
		ID:              row.ID,
		ReviewerID:      row.ReviewerID.UUID,
		ReviewerName:    row.ReviewerName.String,
		ReviewerDeleted: row.ReviewerDeleted,
		RevieweeID:      row.RevieweeID.UUID,
		RevieweeName:    row.RevieweeName.String,
		RevieweeDeleted: row.RevieweeDeleted,
		OrderID:         row.OrderID,
		Rating:          int(row.Rating),
		Comment:         row.Comment,
		CreatedAt:       row.CreatedAt,
		DeletedAt:       row.DeletedAt.Time,
	}
}

func toReviewFromUser(row reviewdb.ListReviewsFromUserRow) Review {
	return Review{
		ID:              row.ID,
		ReviewerID:      row.ReviewerID.UUID,
		ReviewerName:    row.ReviewerName.String,
		ReviewerDeleted: row.ReviewerDeleted,
		RevieweeID:      row.RevieweeID.UUID,
		RevieweeName:    row.RevieweeName.String,
		RevieweeDeleted: row.RevieweeDeleted,
		OrderID:         row.OrderID,
		Rating:          int(row.Rating),
		Comment:         row.Comment,
		CreatedAt:       row.CreatedAt,
		DeletedAt:       row.DeletedAt.Time,
	}
}

func toReviewRemovalListed(row reviewdb.ListReviewRemovalsRow) ReviewRemoval {
	return ReviewRemoval{
		ID:               row.ID,
		ReviewID:         row.ReviewID,
		ReviewerID:       row.ReviewerID,
		RevieweeID:       row.RevieweeID,
		OrderID:          row.OrderID,
		Reason:           row.Reason,
		CreatedAt:        row.CreatedAt,
		CreatedBy:        row.CreatedBy.UUID,
		CreatedByName:    row.CreatedByName.String,
		CreatedByDeleted: row.CreatedByDeleted,
	}
}

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
			// Foreign key violation on an insert means a missing reviewer/reviewee.
			// With the new nullable FKs (ON DELETE SET NULL), this only happens
			// at INSERT time when the referenced user doesn't exist.
			// Distinguish by constraint name to map to the correct error.
			switch pgErr.ConstraintName {
			case "reviews_reviewer_id_fkey":
				return errReviewerNotFound
			case "reviews_reviewee_id_fkey":
				return errRevieweeNotFound
			default:
				// Unknown constraint; fall through to internal error.
			}
			return errReviewNotFound
		case "23505":
			return errReviewAlreadyExists
		case "23514", "23P01":
			return errReviewInvalid
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
