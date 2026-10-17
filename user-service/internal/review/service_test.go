package review

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/api"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/reviewdb"
	"github.com/google/uuid"
)

// fakeRepository is a record-keeping Repository that lets a test pin the
// outcomes each persistence call returns. Transactional methods accept the
// bound queries but never touch them.
type fakeRepository struct {
	exists map[uuid.UUID]bool

	listForUser   []Review
	listFromUser  []Review
	countForUser  int64
	countFromUser int64
	listRemovals  []ReviewRemoval
	countRemovals int64
	userExistsErr error
	listErr       error
	countErr      error

	insertErr    error
	findReview   Review
	findErr      error
	softDelErr   error
	removalErr   error
	recalcErr    error
	recalcCount  int
	insertedID   uuid.UUID
	audited      ReviewRemoval
	softDeleted  uuid.UUID
	recalculated uuid.UUID
}

func (f *fakeRepository) UserExists(_ context.Context, id uuid.UUID) (bool, error) {
	if f.userExistsErr != nil {
		return false, f.userExistsErr
	}
	return f.exists[id], nil
}

func (f *fakeRepository) InsertReview(_ context.Context, _ *reviewdb.Queries, input ReviewInput) error {
	f.insertedID = input.ID
	return f.insertErr
}

func (f *fakeRepository) FindReviewLocked(_ context.Context, _ *reviewdb.Queries, id uuid.UUID) (Review, error) {
	if f.findErr != nil {
		return Review{}, f.findErr
	}
	return f.findReview, nil
}

func (f *fakeRepository) SoftDeleteReview(_ context.Context, _ *reviewdb.Queries, id uuid.UUID) error {
	f.softDeleted = id
	return f.softDelErr
}

func (f *fakeRepository) InsertReviewRemoval(_ context.Context, _ *reviewdb.Queries, removal ReviewRemoval) error {
	f.audited = removal
	return f.removalErr
}

func (f *fakeRepository) RecalculateStats(_ context.Context, _ *reviewdb.Queries, revieweeID uuid.UUID) error {
	f.recalcCount++
	f.recalculated = revieweeID
	return f.recalcErr
}

func (f *fakeRepository) ListReviewsForUser(_ context.Context, _ uuid.UUID, _ int, _ int) ([]Review, error) {
	return f.listForUser, f.listErr
}

func (f *fakeRepository) ListReviewsFromUser(_ context.Context, _ uuid.UUID, _ int, _ int) ([]Review, error) {
	return f.listFromUser, f.listErr
}

func (f *fakeRepository) CountReviewsForUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return f.countForUser, f.countErr
}

func (f *fakeRepository) CountReviewsFromUser(_ context.Context, _ uuid.UUID) (int64, error) {
	return f.countFromUser, f.countErr
}

func (f *fakeRepository) ListReviewRemovals(_ context.Context, _, _ int) ([]ReviewRemoval, error) {
	return f.listRemovals, f.listErr
}

func (f *fakeRepository) CountReviewRemovals(_ context.Context) (int64, error) {
	return f.countRemovals, f.countErr
}

func newTestService(repo Repository) *Service {
	withTx := func(_ context.Context, operation func(*reviewdb.Queries) error) error {
		return operation(reviewdb.New(nil))
	}
	return NewService(repo, withTx)
}

func assertExternal(t *testing.T, err error, wantStatus int) {
	t.Helper()
	var external api.ExternalError
	if !errors.As(err, &external) || external.Code() != wantStatus {
		t.Fatalf("want external error with status %d, got: %v", wantStatus, err)
	}
}

var errBoomTest = errors.New("boom")

func TestSubmitReview(t *testing.T) {
	t.Run("self review is rejected", func(t *testing.T) {
		id := uuid.New()
		svc := newTestService(&fakeRepository{exists: map[uuid.UUID]bool{id: true}})
		err := svc.SubmitReview(context.Background(), id, id, uuid.New(), ReviewSubmission{Rating: 5, Comment: "x"})
		assertExternal(t, err, http.StatusBadRequest)
	})
	t.Run("unknown reviewer is rejected", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		svc := newTestService(&fakeRepository{exists: map[uuid.UUID]bool{reviewee: true}})
		err := svc.SubmitReview(context.Background(), reviewer, reviewee, uuid.New(), ReviewSubmission{Rating: 5, Comment: "x"})
		assertExternal(t, err, http.StatusBadRequest)
	})
	t.Run("unknown reviewee is rejected", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		svc := newTestService(&fakeRepository{exists: map[uuid.UUID]bool{reviewer: true}})
		err := svc.SubmitReview(context.Background(), reviewer, reviewee, uuid.New(), ReviewSubmission{Rating: 4, Comment: "ok"})
		assertExternal(t, err, http.StatusBadRequest)
	})
	t.Run("inserts the review and recomputes stats", func(t *testing.T) {
		reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
		fake := &fakeRepository{exists: map[uuid.UUID]bool{reviewer: true, reviewee: true}}
		svc := newTestService(fake)
		if err := svc.SubmitReview(context.Background(), reviewer, reviewee, order, ReviewSubmission{Rating: 4, Comment: "great"}); err != nil {
			t.Fatalf("submit: %v", err)
		}
		if fake.insertedID == uuid.Nil {
			t.Fatal("no review was inserted")
		}
		if fake.recalcCount != 1 || fake.recalculated != reviewee {
			t.Fatalf("recalculation not run for reviewee: calls=%d reviewee=%v", fake.recalcCount, fake.recalculated)
		}
	})
	t.Run("duplicate maps to already exists", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		fake := &fakeRepository{exists: map[uuid.UUID]bool{reviewer: true, reviewee: true}, insertErr: errReviewAlreadyExists}
		svc := newTestService(fake)
		err := svc.SubmitReview(context.Background(), reviewer, reviewee, uuid.New(), ReviewSubmission{Rating: 3, Comment: "again"})
		assertExternal(t, err, http.StatusConflict)
		if fake.recalcCount != 0 {
			t.Fatal("stats must not be recomputed when the insert fails")
		}
	})
	t.Run("insert failure that is not a duplicate propagates", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		fake := &fakeRepository{exists: map[uuid.UUID]bool{reviewer: true, reviewee: true}, insertErr: errBoomTest}
		svc := newTestService(fake)
		err := svc.SubmitReview(context.Background(), reviewer, reviewee, uuid.New(), ReviewSubmission{Rating: 3, Comment: "again"})
		if !errors.Is(err, errBoomTest) {
			t.Fatalf("non-duplicate insert failure should propagate, got: %v", err)
		}
		if fake.recalcCount != 0 {
			t.Fatal("stats must not be recomputed when the insert fails")
		}
	})
	t.Run("recalculation failure aborts the transaction", func(t *testing.T) {
		reviewer, reviewee := uuid.New(), uuid.New()
		fake := &fakeRepository{exists: map[uuid.UUID]bool{reviewer: true, reviewee: true}, recalcErr: errBoomTest}
		svc := newTestService(fake)
		err := svc.SubmitReview(context.Background(), reviewer, reviewee, uuid.New(), ReviewSubmission{Rating: 5, Comment: "x"})
		if !errors.Is(err, errBoomTest) {
			t.Fatalf("recalculation error should propagate: %v", err)
		}
	})
}

func TestListReviews(t *testing.T) {
	reviewer, reviewee, order := uuid.New(), uuid.New(), uuid.New()
	reviews := []Review{{ID: uuid.New(), ReviewerID: reviewer, RevieweeID: reviewee, OrderID: order, Rating: 5, CreatedAt: time.Now().UTC()}}
	t.Run("received reviews come with page totals", func(t *testing.T) {
		fake := &fakeRepository{listForUser: reviews, countForUser: 3}
		svc := newTestService(fake)
		page, err := pagination.New(2, 10)
		if err != nil {
			t.Fatal(err)
		}
		result, err := svc.ListReviewsForUser(context.Background(), reviewee, page)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Reviews) != 1 || result.Reviews[0].RevieweeID != reviewee {
			t.Fatalf("unexpected reviews: %+v", result.Reviews)
		}
		if result.PageInfo.TotalItems() != 3 || result.PageInfo.Page() != 2 || result.PageInfo.PageSize() != 10 {
			t.Fatalf("unexpected page info: page=%d size=%d total=%d", result.PageInfo.Page(), result.PageInfo.PageSize(), result.PageInfo.TotalItems())
		}
	})
	t.Run("given reviews come with page totals", func(t *testing.T) {
		fake := &fakeRepository{listFromUser: reviews, countFromUser: 7}
		svc := newTestService(fake)
		page, err := pagination.New(1, 50)
		if err != nil {
			t.Fatal(err)
		}
		result, err := svc.ListReviewsFromUser(context.Background(), reviewer, page)
		if err != nil {
			t.Fatal(err)
		}
		if result.PageInfo.TotalItems() != 7 || result.PageInfo.PageSize() != 50 {
			t.Fatalf("unexpected page info: %+v", result.PageInfo)
		}
	})
}

func TestRemoveReview(t *testing.T) {
	t.Run("soft-deletes the review, audits it, and recomputes stats", func(t *testing.T) {
		reviewer, reviewee, order, id := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		createdBy := uuid.New()
		fake := &fakeRepository{findReview: Review{ID: id, ReviewerID: reviewer, RevieweeID: reviewee, OrderID: order}}
		svc := newTestService(fake)
		if err := svc.RemoveReview(context.Background(), id, "abuse", createdBy); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if fake.softDeleted != id {
			t.Fatalf("wrong review soft-deleted: %v", fake.softDeleted)
		}
		if fake.audited.ReviewID != id {
			t.Fatalf("audit row does not reference the removed review: %+v", fake.audited)
		}
		if fake.audited.ReviewerID != reviewer || fake.audited.RevieweeID != reviewee || fake.audited.OrderID != order || fake.audited.Reason != "abuse" || fake.audited.CreatedBy != createdBy {
			t.Fatalf("audit row not captured correctly: %+v", fake.audited)
		}
		if fake.recalcCount != 1 || fake.recalculated != reviewee {
			t.Fatalf("stats not recomputed after removal: %+v", fake)
		}
	})
	t.Run("unknown review is not found", func(t *testing.T) {
		fake := &fakeRepository{findErr: errReviewNotFound}
		svc := newTestService(fake)
		err := svc.RemoveReview(context.Background(), uuid.New(), "reason", uuid.New())
		if !errors.Is(err, errReviewNotFound) {
			t.Fatalf("want review-not-found sentinel, got: %v", err)
		}
		assertExternal(t, err, http.StatusNotFound)
	})
}

func TestListReviewRemovals(t *testing.T) {
	removals := []ReviewRemoval{{ID: uuid.New(), Reason: "abuse"}}
	fake := &fakeRepository{listRemovals: removals, countRemovals: 3}
	svc := newTestService(fake)
	page, err := pagination.New(2, 10)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.ListReviewRemovals(context.Background(), page)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Removals) != 1 || got.Removals[0].Reason != "abuse" {
		t.Fatalf("unexpected removals: %+v", got.Removals)
	}
	if got.TotalItems() != 3 || got.Page() != 2 || got.PageSize() != 10 {
		t.Fatalf("unexpected page info: page=%d size=%d total=%d", got.Page(), got.PageSize(), got.TotalItems())
	}
}
