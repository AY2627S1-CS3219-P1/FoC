// Package events applies the Kafka events Credit consumes. Handlers return
// an error only for failures that redelivery can fix; pkg/kafka then stops
// without committing the offset.
package events

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/reservation"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	ordereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
)

// Topics, as provisioned in infra/kafka/topics.conf.
const (
	OrderOutcomeTopic = "order.outcome"
	UserCreatedTopic  = "users.create"
)

// abortedAttempts bounds retries of a settlement chosen as a deadlock victim.
const abortedAttempts = 3

// Settler lists the settlement operation the outcome handler calls.
type Settler interface {
	ApplyOutcome(ctx context.Context, o reservation.OrderOutcome) (reservation.Reservation, bool, error)
}

// Outcomes settles or releases reservations from order.outcome, the only
// terminal settlement trigger.
type Outcomes struct {
	Settler Settler
	// Backoff is the wait before retrying an aborted settlement.
	Backoff time.Duration
}

var outcomeTypes = map[ordereventsv1.OrderOutcomeType]reservation.OutcomeType{
	ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_COMPLETED:               reservation.OrderCompleted,
	ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_BEFORE_PICKUP: reservation.OrderCancelledBeforePickup,
	ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_CANCELLED_AFTER_PICKUP:  reservation.OrderCancelledAfterPickup,
	ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED:                 reservation.OrderExpired,
	ordereventsv1.OrderOutcomeType_ORDER_OUTCOME_TYPE_FAILED:                  reservation.OrderFailed,
}

// mismatches are outcomes that contradict Credit's records or the contract.
// Redelivery cannot fix them, so they are logged for reconciliation and
// acknowledged instead of blocking the partition.
var mismatches = []error{
	reservation.ErrOutcomeMismatch,
	reservation.ErrReservationNotFound,
	reservation.ErrReservationNotAssigned,
	reservation.ErrInvalidArgument,
}

// Handle applies one order.outcome. Settled and released reservations never
// change, so a redelivered or repeated outcome changes nothing.
func (h *Outcomes) Handle(ctx context.Context, meta kafka.Metadata, event *ordereventsv1.OrderOutcome) error {
	log := slog.With(
		"topic", meta.Topic,
		"partition", meta.Partition,
		"offset", meta.Offset,
		"event_id", event.GetEventId(),
		"order_id", event.GetOrderId(),
		"outcome", event.GetOutcome().String(),
	)

	var (
		applied bool
		err     error
	)
	for attempt := 1; ; attempt++ {
		_, applied, err = h.Settler.ApplyOutcome(ctx, Outcome(event))
		if !errors.Is(err, reservation.ErrAborted) || attempt == abortedAttempts {
			break
		}
		if err := sleep(ctx, time.Duration(attempt)*h.Backoff); err != nil {
			return err
		}
	}

	for _, mismatch := range mismatches {
		if errors.Is(err, mismatch) {
			log.ErrorContext(
				ctx,
				"Order outcome does not match Credit's records; acknowledged without settling",
				"error",
				err,
			)
			return nil
		}
	}
	if err != nil {
		return err
	}

	log.InfoContext(ctx, "Order outcome consumed", "applied", applied)
	return nil
}

// Outcome converts an event into the domain's transport-neutral form. Unset
// fields stay zero, and the domain rejects what an outcome type requires.
func Outcome(event *ordereventsv1.OrderOutcome) reservation.OrderOutcome {
	o := reservation.OrderOutcome{
		OrderID:     event.GetOrderId(),
		Type:        outcomeTypes[event.GetOutcome()],
		RequesterID: event.GetRequesterId(),
		CourierID:   event.GetCourierId(),
	}

	if event.DeliveryDuration != nil {
		o.DeliveryDuration = event.GetDeliveryDuration().AsDuration()
	}
	if event.PickedUpAt != nil {
		o.PickedUpAt = event.GetPickedUpAt().AsTime()
	}
	if event.DeliveredAt != nil {
		o.DeliveredAt = event.GetDeliveredAt().AsTime()
	}
	return o
}

func sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
