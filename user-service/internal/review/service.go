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

// WithTransaction runs operation on the sqlc queries bound to a transaction.
type WithTransaction func(ctx context.Context, operation func(*reviewdb.Queries) error) error

type Service struct {
	repo            Repository
	withTransaction WithTransaction
	// photos may be nil when photo storage is not configured.
	photos PhotoStorage
}

func NewService(repo Repository, withTransaction WithTransaction, photos PhotoStorage) *Service {
	return &Service{repo: repo, withTransaction: withTransaction, photos: photos}
}

// SubmitReview inserts the review, attaches photos and recomputes stats in one transaction.
func (s *Service) SubmitReview(
	ctx context.Context,
	reviewerID, revieweeID, orderID uuid.UUID,
	submission ReviewSubmission,
	photoIDs []uuid.UUID,
) error {
	if reviewerID == revieweeID {
		return errs.WrapBadRequestError(errSelfReview, "cannot review yourself")
	}
	participants, err := s.repo.CheckReviewParticipants(ctx, reviewerID, revieweeID)
	if err != nil {
		return err
	}
	if !participants.ReviewerExists {
		return errs.WrapBadRequestError(errReviewerNotFound, "reviewer does not exist")
	}
	if !participants.RevieweeExists {
		return errs.WrapBadRequestError(errRevieweeNotFound, "reviewee does not exist")
	}
	// A prior submission committed, usually a lost-response retry; the pre-check
	// runs before photo preparation so the retry gets the canonical 409.
	exists, err := s.repo.ReviewExists(ctx, reviewerID, orderID)
	if err != nil {
		return err
	}
	if exists {
		return errs.WrapAlreadyExistsError(errReviewAlreadyExists, "review already exists")
	}
	prepared, err := s.preparePhotos(ctx, reviewerID, photoIDs)
	if err != nil {
		return err
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
				// Anything else is an internal error, not a duplicate.
				return err
			}
		}
		if err := s.attachPhotos(ctx, q, reviewerID, id, prepared); err != nil {
			return err
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
	reviews, err = s.fillPhotos(ctx, reviews)
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
	reviews, err = s.fillPhotos(ctx, reviews)
	if err != nil {
		return ReviewPage{}, err
	}
	return ReviewPage{Reviews: reviews, PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total)}, nil
}

// ListMyReviewsReceived lists the reviews the caller received, newest first.
func (s *Service) ListMyReviewsReceived(
	ctx context.Context,
	userID uuid.UUID,
	page pagination.Pagination,
) (ReviewPage, error) {
	return s.ListReviewsForUser(ctx, userID, page)
}

// RemoveReview soft-deletes the review, records the audit row and recomputes stats.
// ListMyReviewsForOrders returns which of the requests the reviewer has reviewed.
func (s *Service) ListMyReviewsForOrders(
	ctx context.Context,
	reviewerID uuid.UUID,
	orderIDs []uuid.UUID,
) (ReviewOrderStatuses, error) {
	return s.repo.ListReviewOrderStatuses(ctx, reviewerID, orderIDs)
}

// GetMyReviewForOrder returns the reviewer's live review for one order, with
// freshly minted photo URLs.
func (s *Service) GetMyReviewForOrder(ctx context.Context, reviewerID, orderID uuid.UUID) (Review, error) {
	review, err := s.repo.ReviewForOrder(ctx, reviewerID, orderID)
	if err != nil {
		return Review{}, err
	}
	filled, err := s.fillPhotos(ctx, []Review{review})
	if err != nil {
		return Review{}, err
	}
	return filled[0], nil
}

// RemoveReview soft-deletes a review, records the audit row and recomputes stats.
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
		return s.repo.RecalculateStats(ctx, q, review.RevieweeID)
	})
}

// RestoreReview undoes the latest unrestored removal of a review, records the
// restoration and recomputes the reviewee's rating (RP12.3). appealID is
// uuid.Nil when no appeal is involved.
func (s *Service) RestoreReview(ctx context.Context, id uuid.UUID, reason string, appealID, actorID uuid.UUID) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > 2000 {
		return errs.NewBadRequestError("restoration reason must contain between 1 and 2000 characters")
	}
	restorationID, err := newID()
	if err != nil {
		return err
	}

	return s.withTransaction(ctx, func(q *reviewdb.Queries) error {
		review, err := s.repo.RestoreReview(ctx, q, id)
		if errors.Is(err, errReviewNotFound) {
			return errs.WrapNotFoundError(err, "removed review not found")
		}
		if err != nil {
			return err
		}
		removalID, err := s.repo.LatestUnrestoredRemoval(ctx, q, id)
		if errors.Is(err, errReviewNotFound) {
			return errs.WrapFailedPreconditionError(err, "review has no removal to restore")
		}
		if err != nil {
			return err
		}

		restoration := ReviewRestoration{
			ID:        restorationID,
			RemovalID: removalID,
			ReviewID:  id,
			AppealID:  appealID,
			Reason:    reason,
			CreatedBy: actorID,
		}
		if err := s.repo.RecordReviewRestoration(ctx, q, restoration); err != nil {
			return err
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
