package order

import "reflect"

// validateFields permits only the fields owned by the selected action. In
// particular cancellation preserves pickup, courier and reservation observations
// used by outcome consumers. New operations must extend this policy explicitly.
func validateFields(before, after Order, action Action) error {
	expected := before
	expected.Status = after.Status
	expected.Revision = after.Revision
	expected.UpdatedAt = after.UpdatedAt

	switch action {
	case Assign, AcceptCounteroffer:
		expected.CourierID = after.CourierID
		expected.AssignedAt = after.AssignedAt
		expected.RewardCredits = after.RewardCredits
		expected.ReservedCredits = after.ReservedCredits
	case ConfirmReservation:
		expected.ReservedCredits = after.ReservedCredits
	case Pickup:
		expected.PickedUpAt = after.PickedUpAt
	case Deliver:
		expected.DeliveredAt = after.DeliveredAt
		expected.UnattendedDelivery = after.UnattendedDelivery
	case ConfirmReceipt:
		expected.CompletedAt = after.CompletedAt
	case Cancel, Expire, Timeout, RefuseReservation:
		expected.PendingCancellationID = after.PendingCancellationID
		expected.ClosedAt = after.ClosedAt
		expected.CloseReason = after.CloseReason
	case Withdraw, Unassign:
		expected.PendingCancellationID = after.PendingCancellationID
		if action == Withdraw {
			expected.RewardCredits = after.RewardCredits
			expected.ReservedCredits = after.ReservedCredits
		}
		if after.CourierID != nil || after.AssignedAt != nil {
			return ErrInvalid
		}
		expected.CourierID = nil
		expected.AssignedAt = nil
	case BeginWithdrawal:
		expected.PendingCancellationID = after.PendingCancellationID
	case ProposeOffer, BeginAssignment, ResolveAssignment:
		// These actions change offers/workflow only, not Order observations.
	case RecordItemLocation:
		expected.ItemLocation = after.ItemLocation
	default:
		return ErrInvalid
	}

	if !reflect.DeepEqual(expected, after) {
		return ErrInvalid
	}

	return nil
}

// Domain values contain optional pointers. Keep the locked baseline independent
// from operation-owned copies so authorization always uses persisted data.
func cloneOrder(o Order) Order {
	o.CourierID = clonePointer(o.CourierID)
	o.PendingCancellationID = clonePointer(o.PendingCancellationID)
	o.Pickup.Floor = clonePointer(o.Pickup.Floor)
	o.Delivery.Floor = clonePointer(o.Delivery.Floor)
	o.DeliveryInstructions = clonePointer(o.DeliveryInstructions)
	o.CloseReason = clonePointer(o.CloseReason)
	o.ItemLocation = clonePointer(o.ItemLocation)

	o.AssignedAt = clonePointer(o.AssignedAt)
	o.PickedUpAt = clonePointer(o.PickedUpAt)
	o.DeliveredAt = clonePointer(o.DeliveredAt)
	o.CompletedAt = clonePointer(o.CompletedAt)
	o.ClosedAt = clonePointer(o.ClosedAt)

	return o
}

func clonePointer[T any](p *T) *T {
	if p == nil {
		return nil
	}

	v := *p
	return &v
}
