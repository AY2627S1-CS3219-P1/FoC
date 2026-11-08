package order

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// unadmittedCreationRepository finds no retained workflow, so Create with a
// deleting caller must refuse before any admission work.
type unadmittedCreationRepository struct{ CreationRepository }

func (unadmittedCreationRepository) WithinCreation(
	_ context.Context,
	fn func(CreationTx) error,
) error {
	return fn(unadmittedCreationTx{})
}

type unadmittedCreationTx struct{ CreationTx }

func (unadmittedCreationTx) LockIdempotencyCaller(context.Context, IdempotencyScope) error {
	return nil
}

func (unadmittedCreationTx) FindCreationKey(context.Context, string, string, time.Time) (*CreationReplay, error) {
	return nil, nil
}

// Deleting callers may resolve existing work and replay retained outcomes, but
// every path that would admit a NEW obligation must refuse them.
func TestDeletingCallerCannotStartNewWork(t *testing.T) {
	t.Run("create order", func(t *testing.T) {
		_, err := NewCreator(unadmittedCreationRepository{}, nil, nil, CreationOptions{}).Create(
			context.Background(),
			Caller{ID: uuid.NewString(), IsDeleting: true},
			creationInput(),
		)
		if !errors.Is(err, ErrDeleting) {
			t.Fatalf("creation admitted: %v", err)
		}
	})

	t.Run("make offer", func(t *testing.T) {
		r, caller, key := replayAssignmentFixture()
		r.key = nil
		caller.IsDeleting = true
		_, err := NewAssigner(r, nil, AssignmentOptions{}).MakeOffer(
			context.Background(), caller, r.order.ID, 50, key)
		if !errors.Is(err, ErrDeleting) || r.writes != 0 {
			t.Fatalf("offer admitted: %v writes=%d", err, r.writes)
		}
	})

	t.Run("direct acceptance", func(t *testing.T) {
		r, caller, key := replayAssignmentFixture()
		r.key = nil
		caller.IsDeleting = true
		_, err := NewAssigner(r, nil, AssignmentOptions{}).AcceptOrder(
			context.Background(), caller, r.order.ID, key)
		if !errors.Is(err, ErrDeleting) || r.writes != 0 {
			t.Fatalf("acceptance admitted: %v writes=%d", err, r.writes)
		}
	})

	t.Run("item image upload", func(t *testing.T) {
		if _, _, err := NewItemImages(nil, nil).CreateUpload(
			context.Background(), Caller{ID: uuid.NewString(), IsDeleting: true}, "image/png", 10,
		); !errors.Is(err, ErrDeleting) {
			t.Fatalf("upload admitted: %v", err)
		}
	})
}

// A retained key replays its first outcome instead of admitting anything new,
// so a deleting caller can still recover an already-committed result.
func TestDeletingCallerRetainedReplaysStillResolve(t *testing.T) {
	t.Run("make offer replay", func(t *testing.T) {
		r, caller, key := replayAssignmentFixture()
		caller.IsDeleting = true
		r.key.Scope.Method = MakeOfferMethod
		r.key.ResourceID = r.offers[0].ID
		r.key.RequestHash = assignmentHash(r.order.ID, 50)
		r.key.ExpiresAt = time.Now().Add(time.Hour)
		result, err := NewAssigner(r, nil, AssignmentOptions{}).MakeOffer(
			context.Background(), caller, r.order.ID, 50, key)
		if err != nil || result.ID != r.offers[0].ID || r.writes != 0 {
			t.Fatalf("retained offer replay refused: %+v %v", result, err)
		}
	})

	t.Run("acceptance replay", func(t *testing.T) {
		r, caller, key := replayAssignmentFixture()
		caller.IsDeleting = true
		result, err := NewAssigner(r, nil, AssignmentOptions{}).AcceptOrder(
			context.Background(), caller, r.order.ID, key)
		if err != nil || result.Phase != AssignmentSucceeded || r.writes != 0 {
			t.Fatalf("retained acceptance replay refused: %+v %v", result, err)
		}
	})
}

// AcceptOffer selects an offer that is already counted as outstanding, so the
// requester progressing it must stay permitted.
func TestDeletingRequesterCanAcceptRetainedOffer(t *testing.T) {
	r, caller, key := replayAssignmentFixture()
	caller.IsDeleting = true
	r.key.Scope.Method = AcceptOrderMethod
	// AcceptOffer takes the offer ID; the requester is the caller.
	r.order.RequesterID = caller.ID
	if _, err := NewAssigner(r, nil, AssignmentOptions{}).AcceptOffer(
		context.Background(), caller, r.offers[0].ID); errors.Is(err, ErrDeleting) {
		t.Fatalf("existing-offer selection refused for a deleting requester: %v (key %s)", err, key)
	}
}
