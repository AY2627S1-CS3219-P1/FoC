package warning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
)

const CourierWarningRequestedTopic = "order.courier_warning_requested"

type CourierWarningLogic interface {
	RecordCourierWarning(context.Context, CourierWarningRequest) error
}

type CourierWarningConsumer struct {
	Logic CourierWarningLogic
}

// Handle records a courier warning. Events that can never succeed — a nil
// payload, an unsupported reason, an invalid timestamp, malformed fields or a
// courier without a user account — are logged and acknowledged, so one
// unprocessable delivery cannot stall the subscription. Any other failure is
// returned so the subscriber stops and the delivery is redelivered.
func (c *CourierWarningConsumer) Handle(
	ctx context.Context,
	meta kafka.Metadata,
	event *eventsv1.CourierWarningRequested,
) error {
	if event == nil {
		slog.ErrorContext(ctx,
			"Courier warning event is nil; acknowledged without recording a warning",
			"topic", meta.Topic, "partition", meta.Partition, "offset", meta.Offset)
		return nil
	}
	log := slog.With(
		"topic", meta.Topic, "partition", meta.Partition, "offset", meta.Offset,
		"event_id", event.GetEventId(), "courier_id", event.GetCourierId(),
		"order_id", event.GetOrderId(),
	)

	reason, err := warningReason(event.GetReason())
	if err != nil {
		log.ErrorContext(ctx,
			"Courier warning event has an unsupported reason; acknowledged without recording a warning",
			"error", err)
		return nil
	}
	if err := event.GetOccurredAt().CheckValid(); err != nil {
		wrapped := fmt.Errorf("%w: invalid occurrence time: %v", ErrInvalidRequest, err)
		log.ErrorContext(ctx,
			"Courier warning event has an invalid occurrence time; acknowledged without recording a warning",
			"error", wrapped)
		return nil
	}

	err = c.Logic.RecordCourierWarning(ctx, CourierWarningRequest{
		EventID: event.EventId, CourierID: event.CourierId,
		RequestID: event.OrderId, Reason: reason,
		OccurredAt: event.OccurredAt.AsTime(),
	})
	switch {
	case err == nil:
		log.InfoContext(ctx, "Courier warning recorded")
		return nil
	case errors.Is(err, ErrInvalidRequest):
		log.ErrorContext(ctx,
			"Courier warning event has invalid fields; acknowledged without recording a warning",
			"error", err)
		return nil
	case errors.Is(err, ErrUnknownCourier):
		log.ErrorContext(
			ctx,
			"Courier warning event references a courier without a user account; acknowledged without recording a warning",
			"error",
			err,
		)
		return nil
	default:
		return err
	}
}

func warningReason(reason eventsv1.CourierWarningReason) (string, error) {
	switch reason {
	case eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP:
		return "Courier cancelled after pickup", nil
	case eventsv1.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD:
		return "Delivery timed out with no payable reward", nil
	default:
		return "", fmt.Errorf("%w: unsupported courier warning reason %s", ErrInvalidRequest, reason)
	}
}

func RegisterCourierWarningConsumer(subscriber *kafka.Subscriber, consumer *CourierWarningConsumer) error {
	return kafka.Register(subscriber, CourierWarningRequestedTopic,
		func() *eventsv1.CourierWarningRequested { return new(eventsv1.CourierWarningRequested) },
		consumer.Handle)
}
