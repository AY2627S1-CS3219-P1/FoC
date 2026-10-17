package review

import (
	"context"
	"errors"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// WithTransaction runs operation inside a transaction, giving it the review
// queries bound to that transaction. It is wired to the service's GORM pool in
// the composition root.
type WithTransaction func(ctx context.Context, operation func(*reviewdb.Queries) error) error

type Service struct {
	repo            Repository
	withTransaction WithTransaction
}

func NewService(repo Repository, withTransaction WithTransaction) *Service {
	return &Service{repo: repo, withTransaction: withTransaction}
}

// SubmitReview records one participant's review of the other and, in the same
// transaction, recomputes the reviewee's stats so they are current when it
// returns.
func (s *Service) SubmitReview(ctx context.Context, reviewerID, revieweeID, orderID uuid.UUID, submission ReviewSubmission) error {
	if reviewerID == revieweeID {
		return errs.WrapBadRequestError(errSelfReview, "cannot review yourself")
	}
	ok, err := s.repo.UserExists(ctx, reviewerID)
	if err != nil {
		return err
	}
	if !ok {
		return errs.WrapBadRequestError(errReviewerNotFound, "reviewer does not exist")
	}
	ok, err = s.repo.UserExists(ctx, revieweeID)
	if err != nil {
		return err
	}
	if !ok {
		return errs.WrapBadRequestError(errRevieweeNotFound, "reviewee does not exist")
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

// ListReviewsForUser returns the reviews received by a user on a page.
func (s *Service) ListReviewsForUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error) {
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

// ListReviewFromUser returns the reviews a user gave on a page.
func (s *Service) ListReviewFromUser(ctx context.Context, userID uuid.UUID, page pagination.Pagination) (ReviewPage, error) {
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

// RemoveReview hard-deletes a review and records the audit row, then recomputes
// the reviewee's stats -- all atomically.
func (s *Service) RemoveReview(ctx context.Context, id uuid.UUID, reason string, createdBy uuid.UUID) error {
	removal, err := newRemoval(id, reason, createdBy)
	if err != nil {
		return err
	}
	return s.withTransaction(ctx, func(q *reviewdb.Queries) error {
		review, err := s.repo.FindReview(ctx, q, id)
		if err != nil {
			if errors.Is(err, errReviewNotFound) {
				return errs.WrapNotFoundError(errReviewNotFound, "review not found")
			}
			return err
		}
		// The audit row records who was reviewed and for which request.
		removal.ReviewerID = review.ReviewerID
		removal.RevieweeID = review.RevieweeID
		removal.OrderID = review.OrderID
		if err := s.repo.InsertReviewRemoval(ctx, q, removal); err != nil {
			return err
		}
		if err := s.repo.DeleteReview(ctx, q, id); err != nil {
			return err
		}
		return s.repo.RecalculateStats(ctx, q, review.RevieweeID)
	})
}

// ListReviewRemovals returns a page of audit rows for removed reviews, newest
// first.
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

// newRemoval builds the audit row for a removal, generating its UUIDv7 id.
func newRemoval(id uuid.UUID, reason string, createdBy uuid.UUID) (ReviewRemoval, error) {
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
