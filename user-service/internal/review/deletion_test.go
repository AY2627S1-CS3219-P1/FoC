package review

import (
	"context"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

type deletedRecipientRepository struct {
	Repository
	removal ReviewRemoval
	recalculated bool
}

func (r *deletedRecipientRepository) SoftDeleteReview(context.Context, *reviewdb.Queries, uuid.UUID) (Review, error) {
	return Review{ID: uuid.New(), OrderID: uuid.New(), ReviewerID: uuid.New()}, nil
}

func (r *deletedRecipientRepository) RecordReviewRemoval(_ context.Context, _ *reviewdb.Queries, removal ReviewRemoval) error {
	r.removal = removal
	return nil
}

func (r *deletedRecipientRepository) RecalculateStats(context.Context, *reviewdb.Queries, uuid.UUID) error {
	r.recalculated = true
	return nil
}

func TestRemovingRetainedReviewDoesNotRecreateDeletedRecipient(t *testing.T) {
	repository := &deletedRecipientRepository{}
	s := NewService(repository, func(ctx context.Context, operation func(*reviewdb.Queries) error) error { return operation(nil) })
	if err := s.RemoveReview(context.Background(), uuid.New(), "moderated later", uuid.New()); err != nil {
		t.Fatal(err)
	}
	if repository.recalculated || repository.removal.RevieweeID != uuid.Nil || repository.removal.ReviewID == uuid.Nil {
		t.Fatalf("incorrect retained-review removal: %+v", repository)
	}
}

func TestRetainedReviewDoesNotLinkToDeletedProfile(t *testing.T) {
	message := toReviewProto(Review{
		ID: uuid.New(), ReviewerID: uuid.New(), ReviewerDeleted: true,
		RevieweeID: uuid.New(), OrderID: uuid.New(),
	})
	if message.ReviewerId != "" || !message.ReviewerDeleted || message.RevieweeId == "" {
		t.Fatalf("incorrect missing-user presentation: %+v", message)
	}
}
