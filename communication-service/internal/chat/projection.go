package chat

import (
	"context"
	"log/slog"
	"strings"
	"time"

	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
)

// OrderStore saves the local participant copies.
type OrderStore interface {
	// SaveOrder stores o unless the stored copy has the same or a newer
	// revision, so replays and out-of-order events change nothing.
	SaveOrder(ctx context.Context, o Order, now time.Time) error
}

// Projection keeps OrderStore in step with order.status_changed. It runs in
// the notifications consumer, before notifications are sent.
type Projection struct {
	Store OrderStore
	Log   *slog.Logger
	Now   func() time.Time
}

// Apply stores the participants carried by one status change. Malformed
// events are logged and skipped, since replaying can't fix them.
func (p *Projection) Apply(ctx context.Context, e *eventsv1.OrderStatusChanged) error {
	orderID, err1 := uuid.Parse(e.GetOrderId())
	requesterID, err2 := uuid.Parse(e.GetRequesterId())
	if err1 != nil || err2 != nil {
		p.Log.Error("skipping order participants with invalid IDs", "event_id", e.GetEventId())
		return nil
	}
	var courierID uuid.UUID
	if e.CourierId != nil {
		if courierID, err1 = uuid.Parse(e.GetCourierId()); err1 != nil {
			p.Log.Error("skipping order participants with invalid courier ID", "event_id", e.GetEventId())
			return nil
		}
	}

	status := strings.ToLower(strings.TrimPrefix(e.GetStatus().String(), "ORDER_STATUS_"))
	o := Order{ID: orderID, RequesterID: requesterID, CourierID: courierID, Status: status,
		Revision: e.GetRevision(), PickupName: e.GetPickupLocationName()}
	switch e.GetStatus() {
	case eventsv1.OrderStatus_ORDER_STATUS_COMPLETED, eventsv1.OrderStatus_ORDER_STATUS_CANCELLED,
		eventsv1.OrderStatus_ORDER_STATUS_EXPIRED, eventsv1.OrderStatus_ORDER_STATUS_FAILED,
		eventsv1.OrderStatus_ORDER_STATUS_REJECTED:
		o.EndedAt = e.GetOccurredAt().AsTime()
	}
	return p.Store.SaveOrder(ctx, o, p.Now())
}
