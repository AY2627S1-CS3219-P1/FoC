package review

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api/errs"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
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

// SubmitReview commits the pending review and submission outbox atomically.
// Pending reviews contribute nothing to ratings.
func (s *Service) SubmitReview(
	ctx context.Context,
	reviewerID, revieweeID, orderID uuid.UUID,
	submission ReviewSubmission,
) (SubmissionResult, error) {
	if reviewerID == revieweeID {
		return SubmissionResult{}, errs.WrapBadRequestError(errSelfReview, "cannot review yourself")
	}
	id, err := newID()
	if err != nil {
		return SubmissionResult{}, err
	}
	requestID, err := newID()
	if err != nil {
		return SubmissionResult{}, err
	}
	eventID, err := newID()
	if err != nil {
		return SubmissionResult{}, err
	}

	var result SubmissionResult
	err = s.withTransaction(ctx, func(q *reviewdb.Queries) error {
		participants, err := s.repo.CheckReviewParticipants(ctx, q, reviewerID, revieweeID)
		if err != nil {
			return err
		}
		if !participants.ReviewerExists {
			return errs.WrapBadRequestError(errReviewerNotFound, "reviewer does not exist")
		}
		if !participants.RevieweeExists {
			return errs.WrapBadRequestError(errRevieweeNotFound, "reviewee does not exist")
		}

		inserted, err := s.repo.InsertReview(ctx, q, ReviewInput{
			ID:                  id,
			ReviewerID:          reviewerID,
			RevieweeID:          revieweeID,
			OrderID:             orderID,
			Rating:              submission.Rating,
			Comment:             submission.Comment,
			ModerationRequestID: requestID,
		})
		if err != nil {
			return mapInsertReviewError(err)
		}

		// The payload is built from the saved review: the reviewer comes from
		// the authenticated caller, never from a client-supplied field.
		payload, err := encodeSubmittedPayload(&events.ReviewSubmitted{
			EventId:             eventID.String(),
			ModerationRequestId: requestID.String(),
			ReviewId:            id.String(),
			ReviewerId:          reviewerID.String(),
			ContentRevision:     int32(inserted.ContentRevision),
			Comment:             submission.Comment,
			OccurredAt:          timestamppb.New(inserted.CreatedAt),
		})
		if err != nil {
			return err
		}
		if err := s.repo.AppendSubmittedOutbox(ctx, q, SubmittedOutboxRow{
			EventID:        eventID,
			ReviewID:       id,
			PayloadVersion: submittedPayloadVersion,
			Payload:        payload,
		}); err != nil {
			return err
		}

		result = SubmissionResult{
			ReviewID:            id,
			ModerationRequestID: requestID,
			Status:              inserted.ModerationStatus,
		}
		return nil
	})
	if err != nil {
		return SubmissionResult{}, err
	}
	return result, nil
}

func mapInsertReviewError(err error) error {
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

func (s *Service) ListUnpublishedReviewsForUser(
	ctx context.Context,
	reviewerID uuid.UUID,
	page pagination.Pagination,
) (UnpublishedReviewPage, error) {
	reviews, err := s.repo.ListUnpublishedReviewsForUser(ctx, reviewerID, page.Offset(), page.PageSize())
	if err != nil {
		return UnpublishedReviewPage{}, err
	}
	total, err := s.repo.CountUnpublishedReviewsForUser(ctx, reviewerID)
	if err != nil {
		return UnpublishedReviewPage{}, err
	}
	return UnpublishedReviewPage{
		Reviews:  reviews,
		PageInfo: pagination.NewPageInfo(page.Page(), page.PageSize(), total),
	}, nil
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
		if review.ModerationStatus == ModerationApproved {
			return s.repo.RecalculateStats(ctx, q, review.RevieweeID)
		}
		return nil
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
