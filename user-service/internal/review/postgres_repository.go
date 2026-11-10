package review

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
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
		ReportID:   uuid.NullUUID{UUID: removal.ReportID, Valid: removal.ReportID != uuid.Nil},
	}))
}

func (r *PostgresRepository) ReviewFacts(ctx context.Context, id uuid.UUID) (Facts, error) {
	row, err := r.queries.GetReviewFacts(ctx, id)
	if err != nil {
		return Facts{}, mapReviewError("review facts", err)
	}
	return Facts{
		ReviewerID:      row.ReviewerID,
		RevieweeID:      row.RevieweeID,
		OrderID:         row.OrderID,
		Removed:         row.Removed,
		RemovalReportID: row.RemovalReportID.UUID,
	}, nil
}

func (r *PostgresRepository) RestoreReview(ctx context.Context, q *reviewdb.Queries, id uuid.UUID) (Review, error) {
	row, err := q.RestoreReview(ctx, id)
	if err != nil {
		return Review{}, mapReviewError("restore review", err)
	}
	return toReview(row), nil
}

func (r *PostgresRepository) LatestUnrestoredRemoval(
	ctx context.Context,
	q *reviewdb.Queries,
	reviewID uuid.UUID,
) (uuid.UUID, error) {
	id, err := q.LatestUnrestoredRemoval(ctx, reviewID)
	if err != nil {
		return uuid.Nil, mapReviewError("find unrestored review removal", err)
	}
	return id, nil
}

func (r *PostgresRepository) RecordReviewRestoration(
	ctx context.Context,
	q *reviewdb.Queries,
	restoration ReviewRestoration,
) error {
	return mapReviewError("record review restoration", q.RecordReviewRestoration(ctx,
		reviewdb.RecordReviewRestorationParams{
			ID:        restoration.ID,
			RemovalID: restoration.RemovalID,
			ReviewID:  restoration.ReviewID,
			AppealID:  uuid.NullUUID{UUID: restoration.AppealID, Valid: restoration.AppealID != uuid.Nil},
			Reason:    restoration.Reason,
			CreatedBy: restoration.CreatedBy,
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

func (r *PostgresRepository) ListReviewOrderStatuses(
	ctx context.Context,
	reviewerID uuid.UUID,
	orderIDs []uuid.UUID,
) (ReviewOrderStatuses, error) {
	rows, err := r.queries.ListReviewOrderStatuses(ctx, reviewdb.ListReviewOrderStatusesParams{
		ReviewerID: reviewerID,
		OrderIds:   orderIDs,
	})
	if err != nil {
		return ReviewOrderStatuses{}, fmt.Errorf("list review order statuses: %w", err)
	}
	var out ReviewOrderStatuses
	for _, row := range rows {
		id, parseErr := uuid.Parse(row.OrderID)
		if parseErr != nil {
			return ReviewOrderStatuses{}, fmt.Errorf("parse reviewed order id %q: %w", row.OrderID, parseErr)
		}
		if row.Removed {
			out.RemovedOrderIDs = append(out.RemovedOrderIDs, id)
		} else {
			out.ReviewedOrderIDs = append(out.ReviewedOrderIDs, id)
		}
	}
	return out, nil
}

func (r *PostgresRepository) ReviewForOrder(ctx context.Context, reviewerID, orderID uuid.UUID) (Review, error) {
	row, err := r.queries.GetReviewByReviewerAndOrder(ctx, reviewdb.GetReviewByReviewerAndOrderParams{
		ReviewerID: reviewerID,
		OrderID:    orderID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Review{}, errReviewNotFound
		}
		return Review{}, fmt.Errorf("get review for order: %w", err)
	}
	if row.DeletedAt.Valid {
		return Review{}, errReviewRemoved
	}
	return toReviewFromUser(reviewdb.ListReviewsFromUserRow(row)), nil
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

func (r *PostgresRepository) ListMyReviewRemovals(
	ctx context.Context,
	reviewerID uuid.UUID,
	offset, limit int64,
) ([]ReviewRemoval, error) {
	rows, err := r.queries.ListMyReviewRemovals(ctx, reviewdb.ListMyReviewRemovalsParams{
		ReviewerID: reviewerID,
		PageOffset: offset,
		PageLimit:  limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list my removals: %w", err)
	}

	out := make([]ReviewRemoval, len(rows))
	for i, row := range rows {
		out[i] = toReviewRemovalListed(reviewdb.ListReviewRemovalsRow(row))
	}
	return out, nil
}

func (r *PostgresRepository) CountMyReviewRemovals(ctx context.Context, reviewerID uuid.UUID) (int64, error) {
	count, err := r.queries.CountMyReviewRemovals(ctx, reviewerID)
	if err != nil {
		return 0, fmt.Errorf("count my removals: %w", err)
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

func (r *PostgresRepository) ReviewExists(ctx context.Context, reviewerID, orderID uuid.UUID) (bool, error) {
	exists, err := r.queries.ReviewExists(ctx, reviewdb.ReviewExistsParams{
		ReviewerID: reviewerID,
		OrderID:    orderID,
	})
	if err != nil {
		return false, fmt.Errorf("check review exists: %w", err)
	}
	return exists, nil
}

func (r *PostgresRepository) InsertReviewPhoto(ctx context.Context, input ReviewPhotoInput) error {
	err := r.queries.InsertReviewPhoto(ctx, reviewdb.InsertReviewPhotoParams{
		ID:              input.ID,
		OwnerID:         input.OwnerID,
		ObjectKey:       input.ObjectKey,
		ContentType:     input.ContentType,
		SizeBytes:       input.SizeBytes,
		UploadExpiresAt: input.UploadExpiresAt,
	})
	return mapPhotoError("insert review photo", err)
}

func (r *PostgresRepository) GetReviewPhoto(ctx context.Context, ownerID, id uuid.UUID) (ReviewPhoto, error) {
	row, err := r.queries.GetReviewPhoto(ctx, reviewdb.GetReviewPhotoParams{ID: id, OwnerID: ownerID})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReviewPhoto{}, errPhotoNotFound
		}
		return ReviewPhoto{}, fmt.Errorf("get review photo: %w", err)
	}
	return toReviewPhoto(row), nil
}

func (r *PostgresRepository) AttachReviewPhoto(
	ctx context.Context,
	q *reviewdb.Queries,
	ownerID, reviewID uuid.UUID,
	ordinal int,
	photo ReviewPhoto,
) (int64, error) {
	n, err := q.AttachReviewPhoto(ctx, reviewdb.AttachReviewPhotoParams{
		ReviewID:    reviewID,
		Ordinal:     sql.NullInt32{Int32: int32(ordinal), Valid: true},
		ID:          photo.ID,
		OwnerID:     ownerID,
		ObjectKey:   photo.ObjectKey,
		ContentType: photo.ContentType,
		SizeBytes:   photo.SizeBytes,
	})
	if err != nil {
		return 0, fmt.Errorf("attach review photo: %w", err)
	}
	return n, nil
}

func (r *PostgresRepository) ListReviewPhotosForReviews(
	ctx context.Context,
	reviewIDs []uuid.UUID,
) ([]ReviewPhotoAttached, error) {
	rows, err := r.queries.ListReviewPhotosForReviews(ctx, reviewIDs)
	if err != nil {
		return nil, fmt.Errorf("list review photos: %w", err)
	}
	out := make([]ReviewPhotoAttached, len(rows))
	for i, row := range rows {
		out[i] = toReviewPhotoAttached(row)
	}
	return out, nil
}

func (r *PostgresRepository) ClaimOrphanReviewPhotos(
	ctx context.Context,
	createdBefore, now time.Time,
) ([]ClaimedReviewPhoto, error) {
	rows, err := r.queries.ClaimOrphanReviewPhotos(ctx, reviewdb.ClaimOrphanReviewPhotosParams{
		CreatedBefore: createdBefore,
		NowAt:         now,
	})
	if err != nil {
		return nil, fmt.Errorf("claim orphan review photos: %w", err)
	}
	out := make([]ClaimedReviewPhoto, len(rows))
	for i, row := range rows {
		out[i] = ClaimedReviewPhoto{ID: row.ID, ObjectKey: row.ObjectKey}
	}
	return out, nil
}

func (r *PostgresRepository) DeleteOrphanReviewPhoto(ctx context.Context, id uuid.UUID) (int64, error) {
	n, err := r.queries.DeleteOrphanReviewPhoto(ctx, id)
	if err != nil {
		return 0, fmt.Errorf("delete orphan review photo row: %w", err)
	}
	return n, nil
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

// mapPhotoError translates persistence failures of photo rows. Unlike reviews,
// a missing photo row is an expected client-outcome ("not staged"), not a
// review lookup miss.
func mapPhotoError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return errPhotoNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			// owner_id references a missing user.
			return errPhotoNotFound
		case "23514", "23P01":
			return errs.NewBadRequestError("photo metadata violates the accepted limits")
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func toReviewPhoto(row reviewdb.ReviewPhoto) ReviewPhoto {
	return ReviewPhoto{
		ID:              row.ID,
		OwnerID:         row.OwnerID,
		ObjectKey:       row.ObjectKey,
		ContentType:     row.ContentType,
		SizeBytes:       row.SizeBytes,
		State:           row.State,
		ReviewID:        row.ReviewID.UUID,
		Ordinal:         int(row.Ordinal.Int32),
		CreatedAt:       row.CreatedAt,
		UploadExpiresAt: row.UploadExpiresAt,
	}
}

func toReviewPhotoAttached(row reviewdb.ListReviewPhotosForReviewsRow) ReviewPhotoAttached {
	return ReviewPhotoAttached{
		ID:          row.ID,
		ReviewID:    row.ReviewID.UUID,
		ObjectKey:   row.ObjectKey,
		ContentType: row.ContentType,
		Ordinal:     int(row.Ordinal.Int32),
	}
}
