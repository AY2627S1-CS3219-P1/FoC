package review

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

var (
	_ Repository       = (*PostgresRepository)(nil)
	_ ModerationStore  = (*PostgresRepository)(nil)
	_ OutboxRepository = (*PostgresRepository)(nil)
)

func (r *PostgresRepository) CheckReviewParticipants(
	ctx context.Context,
	q *reviewdb.Queries,
	reviewerID, revieweeID uuid.UUID,
) (ReviewParticipantExistence, error) {
	exists, err := q.ReviewParticipantsExist(ctx, reviewdb.ReviewParticipantsExistParams{
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

func (r *PostgresRepository) InsertReview(
	ctx context.Context,
	q *reviewdb.Queries,
	input ReviewInput,
) (InsertedReview, error) {
	row, err := q.InsertReview(ctx, reviewdb.InsertReviewParams{
		ID:                  input.ID,
		ReviewerID:          input.ReviewerID,
		RevieweeID:          input.RevieweeID,
		OrderID:             input.OrderID,
		Rating:              int32(input.Rating),
		Comment:             input.Comment,
		ModerationRequestID: input.ModerationRequestID,
	})
	if err != nil {
		return InsertedReview{}, mapReviewError("insert review", err)
	}
	status, err := parseModerationStatus(row.ModerationStatus)
	if err != nil {
		return InsertedReview{}, err
	}
	return InsertedReview{
		CreatedAt:        row.CreatedAt,
		ModerationStatus: status,
		ContentRevision:  int(row.ContentRevision),
	}, nil
}

func (r *PostgresRepository) AppendSubmittedOutbox(
	ctx context.Context,
	q *reviewdb.Queries,
	row SubmittedOutboxRow,
) error {
	return mapReviewError("append review-submitted outbox row", q.InsertReviewSubmittedOutbox(ctx,
		reviewdb.InsertReviewSubmittedOutboxParams{
			EventID:        row.EventID,
			ReviewID:       row.ReviewID,
			PayloadVersion: row.PayloadVersion,
			Payload:        json.RawMessage(row.Payload),
		}))
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

func (r *PostgresRepository) ListUnpublishedReviewsForUser(
	ctx context.Context,
	reviewerID uuid.UUID,
	filter UnpublishedFilter,
	offset, limit int64,
) ([]UnpublishedReview, error) {
	rows, err := r.queries.ListUnpublishedReviewsForUser(ctx, reviewdb.ListUnpublishedReviewsForUserParams{
		ReviewerID:             reviewerID,
		ModerationStatusFilter: statusFilterParam(filter),
		OrderID:                orderFilterParam(filter),
		PageOffset:             offset,
		PageLimit:              limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list unpublished reviews: %w", err)
	}
	out := make([]UnpublishedReview, len(rows))
	for i, row := range rows {
		review, err := toUnpublishedReview(row)
		if err != nil {
			return nil, err
		}
		out[i] = review
	}
	return out, nil
}

func (r *PostgresRepository) CountUnpublishedReviewsForUser(
	ctx context.Context,
	reviewerID uuid.UUID,
	filter UnpublishedFilter,
) (int64, error) {
	count, err := r.queries.CountUnpublishedReviewsForUser(ctx, reviewdb.CountUnpublishedReviewsForUserParams{
		ReviewerID:             reviewerID,
		ModerationStatusFilter: statusFilterParam(filter),
		OrderID:                orderFilterParam(filter),
	})
	if err != nil {
		return 0, fmt.Errorf("count unpublished reviews: %w", err)
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

func (r *PostgresRepository) RecordModerationProcessedEvent(
	ctx context.Context,
	q *reviewdb.Queries,
	eventID uuid.UUID,
) (int64, error) {
	rows, err := q.RecordReviewModerationProcessedEvent(ctx, eventID)
	if err != nil {
		return 0, fmt.Errorf("record moderation processed event: %w", err)
	}
	return rows, nil
}

func (r *PostgresRepository) LockReviewForModeration(
	ctx context.Context,
	q *reviewdb.Queries,
	reviewID uuid.UUID,
) error {
	if err := q.LockReviewForModeration(ctx, reviewID); err != nil {
		return fmt.Errorf("lock review: %w", err)
	}
	return nil
}

func (r *PostgresRepository) GetReviewForModeration(
	ctx context.Context,
	q *reviewdb.Queries,
	reviewID uuid.UUID,
) (ModerationTarget, error) {
	row, err := q.GetReviewForModeration(ctx, reviewID)
	if err != nil {
		return ModerationTarget{}, mapReviewError("get review for moderation", err)
	}
	status, err := parseModerationStatus(row.ModerationStatus)
	if err != nil {
		return ModerationTarget{}, err
	}
	return ModerationTarget{
		ID:                   row.ID,
		ReviewerID:           row.ReviewerID,
		RevieweeID:           row.RevieweeID,
		ModerationStatus:     status,
		ModerationRequestID:  row.ModerationRequestID.UUID,
		ContentRevision:      int(row.ContentRevision),
		AppliedRunGeneration: row.AppliedRunGeneration,
		Deleted:              row.DeletedAt.Valid,
	}, nil
}

func (r *PostgresRepository) ApplyModerationOutcome(
	ctx context.Context,
	q *reviewdb.Queries,
	reviewID uuid.UUID,
	completion ModerationCompletion,
) error {
	codes, err := submittedReasonCodesJSON(completion.ReasonCodes)
	if err != nil {
		return err
	}
	err = q.ApplyReviewModerationOutcome(ctx, reviewdb.ApplyReviewModerationOutcomeParams{
		ModerationStatus:     string(completion.Status),
		ReasonCodes:          codes,
		AppliedRunGeneration: completion.RunGeneration,
		PolicyVersion:        sql.NullString{String: completion.PolicyVersion, Valid: completion.PolicyVersion != ""},
		ConfigVersionID: sql.NullString{
			String: completion.ConfigVersionID,
			Valid:  completion.ConfigVersionID != "",
		},
		ModeratedAt: sql.NullTime{Time: completion.ModeratedAt, Valid: true},
		ReviewID:    reviewID,
	})
	return mapReviewError("apply review moderation outcome", err)
}

func (r *PostgresRepository) ClaimSubmittedOutbox(
	ctx context.Context,
	eligibleAt, leaseUntil time.Time,
) (*SubmittedFact, error) {
	leaseToken, err := newID()
	if err != nil {
		return nil, err
	}
	row, err := r.queries.ClaimReviewSubmittedOutbox(ctx, reviewdb.ClaimReviewSubmittedOutboxParams{
		LeaseToken: leaseToken,
		LeaseUntil: leaseUntil,
		EligibleAt: eligibleAt,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("claim review-submitted outbox row: %w", err)
	}
	if !row.LeaseToken.Valid {
		return nil, errors.New("claimed review-submitted outbox row has no lease token")
	}
	return &SubmittedFact{
		EventID:        row.EventID,
		ReviewID:       row.ReviewID,
		PayloadVersion: row.PayloadVersion,
		Payload:        row.Payload,
		LeaseToken:     row.LeaseToken.UUID,
	}, nil
}

func (r *PostgresRepository) MarkSubmittedOutboxPublished(
	ctx context.Context,
	fact SubmittedFact,
	now time.Time,
) error {
	rows, err := r.queries.MarkReviewSubmittedOutboxPublished(ctx, reviewdb.MarkReviewSubmittedOutboxPublishedParams{
		PublishedAt: now,
		EventID:     fact.EventID,
		LeaseToken:  fact.LeaseToken,
	})
	if err != nil {
		return fmt.Errorf("mark review-submitted outbox published: %w", err)
	}
	if rows != 1 {
		return errors.New("review-submitted outbox lease lost before publication acknowledgement")
	}
	return nil
}

func (r *PostgresRepository) ScheduleSubmittedOutboxRetry(
	ctx context.Context,
	fact SubmittedFact,
	availableAt time.Time,
) error {
	rows, err := r.queries.RescheduleReviewSubmittedOutbox(ctx, reviewdb.RescheduleReviewSubmittedOutboxParams{
		AvailableAt: availableAt,
		EventID:     fact.EventID,
		LeaseToken:  fact.LeaseToken,
	})
	if err != nil {
		return fmt.Errorf("schedule review-submitted outbox retry: %w", err)
	}
	if rows != 1 {
		return errors.New("review-submitted outbox lease lost before retry was scheduled")
	}
	return nil
}

func statusFilterParam(filter UnpublishedFilter) sql.NullString {
	if filter.Status == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*filter.Status), Valid: true}
}

func orderFilterParam(filter UnpublishedFilter) uuid.NullUUID {
	if filter.OrderID == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *filter.OrderID, Valid: true}
}

func toReview(row reviewdb.SoftDeleteReviewRow) Review {
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

func toUnpublishedReview(row reviewdb.ListUnpublishedReviewsForUserRow) (UnpublishedReview, error) {
	status, err := parseModerationStatus(row.ModerationStatus)
	if err != nil {
		return UnpublishedReview{}, err
	}
	codes, err := submittedReasonCodes(row.ReasonCodes)
	if err != nil {
		return UnpublishedReview{}, err
	}
	return UnpublishedReview{
		ID:                    row.ID,
		ReviewerID:            row.ReviewerID,
		ReviewerName:          row.ReviewerName,
		RevieweeID:            row.RevieweeID,
		RevieweeName:          row.RevieweeName,
		OrderID:               row.OrderID,
		Rating:                int(row.Rating),
		Comment:               row.Comment,
		CreatedAt:             row.CreatedAt,
		ModerationStatus:      status,
		ModerationRequestID:   row.ModerationRequestID.UUID,
		ContentRevision:       int(row.ContentRevision),
		ModerationReasonCodes: codes,
		ModeratedAt:           row.ModeratedAt.Time,
	}, nil
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
