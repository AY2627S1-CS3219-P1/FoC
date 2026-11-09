package order

import (
	"context"
	"fmt"
	db "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/database/orderdb"
)

// applyAssignmentHistory is called only by Apply after validating the locked
// aggregate. No feature writer can commit an assignment without its episode.
func (t *postgresTx) applyAssignmentHistory(ctx context.Context, before *Order, c Change) error {
	previous, next := "", ""
	if before != nil && before.CourierID != nil {
		previous = *before.CourierID
	}
	if c.Order.CourierID != nil {
		next = *c.Order.CourierID
	}
	if previous == next {
		return nil
	}
	orderID, err := parseID(c.Order.ID)
	if err != nil {
		return err
	}
	if previous != "" {
		courierID, err := parseID(previous)
		if err != nil {
			return err
		}
		reason := string(c.Action)
		n, err := t.q.CloseAssignmentHistory(ctx, db.CloseAssignmentHistoryParams{
			OrderID: orderID, CourierID: courierID, EndedRevision: &c.Order.Revision,
			EndedAt: &c.Audit.At, EndReason: &reason,
		})
		if err != nil {
			return persistenceError(err)
		}
		if n != 1 {
			return fmt.Errorf("missing current assignment episode: %w", ErrConflict)
		}
	}
	if next != "" {
		if c.Order.AssignedAt == nil {
			return ErrInvalid
		}
		courierID, err := parseID(next)
		if err != nil {
			return err
		}
		return persistenceError(t.q.OpenAssignmentHistory(ctx, db.OpenAssignmentHistoryParams{
			OrderID: orderID, CourierID: courierID, AssignedRevision: &c.Order.Revision, AssignedAt: c.Order.AssignedAt,
		}))
	}
	return nil
}
