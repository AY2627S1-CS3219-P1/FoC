package review

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// WithTransaction runs operation inside a database/sql transaction with the
// sqlc queries bound to it; the composition root wires it to the GORM pool.
type WithTransaction func(ctx context.Context, operation func(*reviewdb.Queries) error) error

type Service struct {
	repo            Repository
	withTransaction WithTransaction
}

func NewService(repo Repository, withTransaction WithTransaction) *Service {
	return &Service{repo: repo, withTransaction: withTransaction}
}

// SubmitReview inserts the review and recomputes the reviewee's stats in the
// same transaction, so the stats are current when it returns.
func (s *Service) SubmitReview(
	ctx context.Context,
	reviewerID, revieweeID, orderID uuid.UUID,
	submission ReviewSubmission,
) error {
	if reviewerID == revieweeID {
		return errs.WrapBadRequestError(errSelfReview, "cannot review yourself")
	}

	id, err := newID()
	if err != nil {
		return err
	}

	input := ReviewInput{
		ID:         id,
		ReviewerID: reviewerID,
		RevieweeID: revieweeID,
		OrderID:    orderID,
		Rating:     submission.Rating,
		Comment:    submission.Comment,
	}

	return s.withTransaction(ctx, func(q *reviewdb.Queries) error {
		// Lock and check participants inside the transaction to prevent a
		// hard-deleted or deleting account from sneaking in between a
		// pre-check and the insert. Deterministic lock order by UUID.
		status, err := s.repo.LockAndCheckParticipants(ctx, q, reviewerID, revieweeID)
		if err != nil {
			return err
		}

		// The reviewer is the authenticated author, so a missing row means the
		// caller's account is no longer live; treat that as unauthenticated.
		if !status.ReviewerExists {
			return errs.WrapUnauthorizedError(errReviewerNotFound, "reviewer does not exist")
		}
		// The reviewee is the target; a missing account is a not-found.
		if !status.RevieweeExists {
			return errs.WrapNotFoundError(errRevieweeNotFound, "reviewee does not exist")
		}

		// Both accounts exist. Reject a permanently deleting author or target:
		// a deleting caller must not start new obligations. A participant that
		// still has a row but is soft-deleted (deleted_at IS NOT NULL) or
		// permanently deleting (is_deleting) is not live.
		if !status.ReviewerLive {
			return errs.WrapFailedPreconditionError(errReviewerDeleting, "reviewer is permanently deleting")
		}
		if !status.RevieweeLive {
			return errs.WrapFailedPreconditionError(errRevieweeDeleting, "reviewee is permanently deleting")
		}

		if err := s.repo.InsertReview(ctx, q, input); err != nil {
			switch {
			case errors.Is(err, errReviewInvalid):
				return errs.WrapBadRequestError(err, "invalid review data")
			case errors.Is(err, errReviewAlreadyExists):
				return errs.WrapAlreadyExistsError(err, "review already exists")
			default:
				// Anything else (foreign keys, connectivity, timeouts) is not a
				// duplicate; surface it as an internal error instead of a 409.
				return err
			}
		}
		return s.repo.RecalculateStats(ctx, q, revieweeID)
	})
}

func (s *Service) ListReviewsForUser(
	ctx context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (ReviewPage, error) {
	reviews, err := s.repo.ListReviewsForUser(ctx, userID, page.Offset(), page.PageSize())
	if err != nil {
		return ReviewPage{}, err
	}
	total, err := s.repo.CountReviewsForUser(ctx, userID)
	if err != nil {
		return ReviewPage{}, err
	}
	return ReviewPage{Reviews: reviews, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

func (s *Service) ListReviewsFromUser(
	ctx context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (ReviewPage, error) {
	reviews, err := s.repo.ListReviewsFromUser(ctx, userID, page.Offset(), page.PageSize())
	if err != nil {
		return ReviewPage{}, err
	}
	total, err := s.repo.CountReviewsFromUser(ctx, userID)
	if err != nil {
		return ReviewPage{}, err
	}
	return ReviewPage{Reviews: reviews, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// RemoveReview soft-deletes the review, records the audit row and recomputes
// the reviewee's stats atomically.
func (s *Service) RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 100000 {
		return errs.NewBadRequestError("removal reason must contain between 1 and 100000 characters")
	}

	removal, err := newRemoval(reason, createdBy)
	if err != nil {
		return err
	}
	return s.withTransaction(ctx, func(q *reviewdb.Queries) error {
		review, err := s.repo.SoftDeleteReview(ctx, q, id)
		if err != nil {
			if errors.Is(err, errReviewNotFound) {
				return errs.WrapNotFoundError(errReviewNotFound, "review not found")
			}
			return err
		}
		removal.ReviewID = review.ID
		removal.ReviewerID = review.ReviewerID
		removal.RevieweeID = review.RevieweeID
		removal.OrderID = review.OrderID
		if err := s.repo.RecordReviewRemoval(ctx, q, removal); err != nil {
			return err
		}
		// Skip the reviewee statistics update when the reviewee no longer exists:
		// its foreign key cleared to NULL on hard delete, so there is no user
		// row to update and none must be manufactured.
		if review.RevieweeID == uuid.Nil {
			return nil
		}
		return s.repo.RecalculateStats(ctx, q, review.RevieweeID)
	})
}

func (s *Service) ListReviewRemovals(ctx context.Context, page pagination.Pagination) (RemovalPage, error) {
	removals, err := s.repo.ListReviewRemovals(ctx, page.Offset(), page.PageSize())
	if err != nil {
		return RemovalPage{}, err
	}
	total, err := s.repo.CountReviewRemovals(ctx)
	if err != nil {
		return RemovalPage{}, err
	}
	return RemovalPage{Removals: removals, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// The surrogate id is unrelated to the review's id and survives a later
// permanent deletion of the review.
func newRemoval(reason string, createdBy uuid.UUID) (ReviewRemoval, error) {
	removalID, err := newID()
	if err != nil {
		return ReviewRemoval{}, err
	}
	return ReviewRemoval{
		ID:        removalID,
		Reason:    reason,
		CreatedBy: createdBy,
	}, nil
}
