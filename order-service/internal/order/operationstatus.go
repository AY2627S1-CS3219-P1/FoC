package order

import (
	"context"

	"github.com/google/uuid"
)

// OperationReader returns retained facts without leasing or resuming work.
type OperationReader interface {
	AssignmentStatus(context.Context, string) (AssignmentAttempt, error)
	CancellationStatus(context.Context, string) (Cancellation, error)
}

type OperationStatus struct{ Reader OperationReader }

func (s *OperationStatus) GetAssignmentAttempt(ctx context.Context, c Caller, id string) (AssignmentAttempt, error) {
	if _, err := uuid.Parse(id); err != nil {
		return AssignmentAttempt{}, ErrInvalid
	}
	a, err := s.Reader.AssignmentStatus(ctx, id)
	if err != nil {
		return AssignmentAttempt{}, err
	}
	if c.ID == "" || a.CallerID != c.ID {
		return AssignmentAttempt{}, ErrNotFound
	}
	return a, nil
}

func (s *OperationStatus) GetCancellation(ctx context.Context, c Caller, id string) (Cancellation, error) {
	if _, err := uuid.Parse(id); err != nil {
		return Cancellation{}, ErrInvalid
	}
	a, err := s.Reader.CancellationStatus(ctx, id)
	if err != nil {
		return Cancellation{}, err
	}
	if c.ID == "" || a.CallerID != c.ID {
		return Cancellation{}, ErrNotFound
	}
	return a, nil
}
