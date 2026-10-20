package order

import (
	"context"
	"encoding/json"
)

const (
	BeginSuspensionRemoval Action = "begin_suspension_removal"
	RemoveSuspendedCourier Action = "remove_suspended_courier"
)

// SuspensionRemoval targets the immutable successful assignment, not whichever
// courier happens to occupy an Order when a delayed instruction arrives.
// DecisionID identifies a persisted, explicit administrator suspension decision.
type SuspensionRemoval struct {
	DecisionID, OrderID, CourierID, AssignmentID string
}

// RemoveSuspendedAssignment is an internal admission boundary for an authorized
// suspension dispatcher. It is deliberately not mounted as a general admin API.
// The caller must establish the suspension decision's authenticity. Nil means
// pickup already won, the assignment changed, or there is no assignment to remove.
// Pending work uses B3's existing cancellation recovery; no Credit call happens
// in the transaction or on a duplicate/delayed instruction.
func (s *Canceller) RemoveSuspendedAssignment(ctx context.Context, target SuspensionRemoval) (*CancellationResult, error) {
	for _, id := range []string{target.DecisionID, target.OrderID, target.CourierID, target.AssignmentID} {
		if parsed, err := parseID(id); err != nil || parsed.String() != id {
			return nil, ErrInvalid
		}
	}
	var result *CancellationResult
	err := s.mutate(ctx, target.OrderID, nil, func(tx CancellationTx, o Order, _ []Offer) (*Change, error) {
		if o.Status != AwaitingPickup || o.PickedUpAt != nil || !o.IsAssignedTo(target.CourierID) {
			return nil, nil
		}
		assignment, err := tx.CurrentSuccessfulAssignment(ctx, o.ID)
		if err != nil {
			return nil, err
		}
		if assignment == nil {
			return nil, ErrPrecondition
		}
		if assignment.ID != target.AssignmentID {
			return nil, nil
		}
		if o.PendingCancellationID != nil {
			pending, err := tx.FindCancellation(ctx, *o.PendingCancellationID)
			if err != nil {
				return nil, err
			}
			if pending.Status != CancellationPending || !matchesWithdrawal(o, pending) || pending.AssignmentID != target.AssignmentID {
				return nil, ErrConflict
			}
			result = &CancellationResult{Order: o, OperationID: pending.ID, Status: pending.Status}
			return nil, nil
		}
		if s.credit == nil {
			return nil, ErrPrecondition
		}
		now := s.now().UTC()
		operation := Cancellation{
			ID: s.newID(), OrderID: o.ID, CallerID: target.CourierID,
			Reason:               "suspension decision " + target.DecisionID,
			SuspensionDecisionID: target.DecisionID,
			Status:               CancellationPending, AssignmentID: assignment.ID,
			CourierID: target.CourierID, AssignedAt: clonePointer(o.AssignedAt),
			OriginalAmount: assignment.OriginalAmount,
			LeaseUntil:     now, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
		}
		after := cloneOrder(o)
		after.PendingCancellationID = &operation.ID
		after.Revision++
		after.UpdatedAt = now
		details, err := json.Marshal(map[string]string{
			"operation_id": operation.ID, "suspension_decision_id": target.DecisionID,
			"assignment_id": target.AssignmentID, "courier_id": target.CourierID,
		})
		if err != nil {
			return nil, err
		}
		change, err := NewChange(&o, after, Actor{Kind: System}, BeginSuspensionRemoval, details, s.newID)
		if err != nil {
			return nil, err
		}
		if err := tx.InsertCancellation(ctx, operation); err != nil {
			return nil, err
		}
		result = &CancellationResult{Order: after, OperationID: operation.ID, Status: operation.Status}
		return &change, nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
