package outbox

import (
	"encoding/json"
	"errors"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func encode(f Fact) (proto.Message, error) {
	if f.Version != 1 {
		return nil, errors.New("unsupported Order fact version")
	}
	var p app.FactsPayload
	if err := json.Unmarshal(f.Payload, &p); err != nil {
		return nil, err
	}
	if p.OrderID != f.OrderID || p.Revision != f.Revision || p.RequesterID == "" || p.At.IsZero() {
		return nil, errors.New("invalid Order fact identity")
	}
	switch app.FactKind(f.Kind) {
	case app.StatusChanged:
		status, ok := eventStatuses[p.Status]
		if !ok {
			return nil, errors.New("invalid Order status")
		}
		previous, ok := eventStatuses[p.PreviousStatus]
		if !ok {
			return nil, errors.New("invalid previous Order status")
		}
		actor, ok := eventActors[f.ActorKind]
		if !ok {
			return nil, errors.New("invalid Order actor")
		}
		return &events.OrderStatusChanged{
			EventId:            f.ID,
			OrderId:            f.OrderID,
			Revision:           f.Revision,
			PreviousStatus:     previous,
			Status:             status,
			Actor:              &events.Actor{Type: actor, UserId: f.ActorID},
			RequesterId:        p.RequesterID,
			CourierId:          p.CourierID,
			PreviousCourierId:  p.PreviousCourierID,
			PickupLocationName: f.PickupName,
			OccurredAt:         timestamppb.New(p.At),
		}, nil
	case app.Outcome:
		outcome, err := outcomeType(p)
		if err != nil {
			return nil, err
		}
		return &events.OrderOutcome{
			EventId:          f.ID,
			OrderId:          f.OrderID,
			Outcome:          outcome,
			RequesterId:      p.RequesterID,
			CourierId:        p.CourierID,
			ReservedCredits:  p.ReservedCredits,
			DeliveryDuration: durationpb.New(time.Duration(p.DeliveryDurationNS)),
			PickedUpAt:       timestamp(p.PickedUpAt),
			DeliveredAt:      timestamp(p.DeliveredAt),
			OccurredAt:       timestamppb.New(p.At),
		}, nil
	case app.CourierWarning:
		if p.CourierID == nil || !p.AfterPickup || p.Status != app.Failed {
			return nil, errors.New("invalid courier warning fact")
		}
		reason := events.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP
		if p.Action == app.Timeout {
			reason = events.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD
		} else if p.Action != app.Cancel {
			return nil, errors.New("unsupported courier warning action")
		}
		return &events.CourierWarningRequested{
			EventId: f.ID, OrderId: f.OrderID, CourierId: *p.CourierID,
			Reason: reason, OccurredAt: timestamppb.New(p.At),
		}, nil
	default:
		return nil, errors.New("unsupported Order fact kind")
	}
}

func outcomeType(p app.FactsPayload) (events.OrderOutcomeType, error) {
	switch p.Status {
	case app.Completed:
		if p.CourierID == nil || p.PickedUpAt == nil || p.DeliveredAt == nil || p.DeliveredAt.Before(*p.PickedUpAt) ||
			p.DeliveryDurationNS <= 0 {
			break
		}
		return events.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED, nil
	case app.Cancelled:
		if p.AfterPickup {
			if p.CourierID == nil || p.PickedUpAt == nil {
				break
			}
			return events.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP, nil
		}
		return events.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP, nil
	case app.Expired:
		return events.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED, nil
	case app.Failed:
		return events.OrderOutcomeType_ORDER_OUTCOME_TYPE_FAILED, nil
	}
	return 0, errors.New("invalid terminal Order outcome")
}

func timestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

var eventActors = map[string]events.ActorType{
	"user":   events.ActorType_ACTOR_TYPE_USER,
	"admin":  events.ActorType_ACTOR_TYPE_ADMIN,
	"system": events.ActorType_ACTOR_TYPE_SYSTEM,
}
var eventStatuses = map[app.Status]events.OrderStatus{
	"":                     events.OrderStatus_ORDER_STATUS_UNSPECIFIED,
	app.PendingReservation: events.OrderStatus_ORDER_STATUS_PENDING_RESERVATION,
	app.AwaitingCourier:    events.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
	app.AwaitingPickup:     events.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
	app.Delivering:         events.OrderStatus_ORDER_STATUS_DELIVERING,
	app.Delivered:          events.OrderStatus_ORDER_STATUS_DELIVERED,
	app.Completed:          events.OrderStatus_ORDER_STATUS_COMPLETED,
	app.Cancelled:          events.OrderStatus_ORDER_STATUS_CANCELLED,
	app.Expired:            events.OrderStatus_ORDER_STATUS_EXPIRED,
	app.Failed:             events.OrderStatus_ORDER_STATUS_FAILED,
	app.Rejected:           events.OrderStatus_ORDER_STATUS_REJECTED,
}
