package order

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

type retainedOperations struct{ owner, id string }

func (r retainedOperations) AssignmentStatus(context.Context, string) (AssignmentAttempt, error) {
	return AssignmentAttempt{ID: r.id, CallerID: r.owner, Phase: AssignmentSucceeded}, nil
}
func (r retainedOperations) CancellationStatus(context.Context, string) (Cancellation, error) {
	return Cancellation{ID: r.id, CallerID: r.owner, Status: CancellationSuperseded}, nil
}

func TestReadOnlyOperationOwnership(t *testing.T) {
	id, owner := uuid.NewString(), uuid.NewString()
	s := &OperationStatus{Reader: retainedOperations{owner, id}}
	ctx := context.Background()
	for _, caller := range []Caller{{ID: owner, Suspended: true}, {ID: uuid.NewString()}, {ID: uuid.NewString(), Admin: true}} {
		a, err := s.GetAssignmentAttempt(ctx, caller, id)
		c, cerr := s.GetCancellation(ctx, caller, id)
		if caller.ID == owner {
			if err != nil || cerr != nil || a.Phase != AssignmentSucceeded || c.Status != CancellationSuperseded {
				t.Fatal(a, c, err, cerr)
			}
		} else if !errors.Is(err, ErrNotFound) || !errors.Is(cerr, ErrNotFound) {
			t.Fatal("non-owner can inspect operation", err, cerr)
		}
	}
}
