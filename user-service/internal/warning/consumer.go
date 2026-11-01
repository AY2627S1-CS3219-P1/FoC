package warning

import (
	"context"
	"fmt"

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

func (c *CourierWarningConsumer) Handle(
	ctx context.Context,
	_ kafka.Metadata,
	event *eventsv1.CourierWarningRequested,
) error {
	if event == nil {
		return fmt.Errorf("courier warning event is nil")
	}

	reason, err := warningReason(event.GetReason())
	if err != nil {
		return err
	}
	if err := event.GetOccurredAt().CheckValid(); err != nil {
		return fmt.Errorf("%w: invalid occurrence time: %v", ErrInvalidRequest, err)
	}

	return c.Logic.RecordCourierWarning(ctx, CourierWarningRequest{
		EventID: event.EventId, CourierID: event.CourierId,
		RequestID: event.OrderId, Reason: reason,
		OccurredAt: event.OccurredAt.AsTime(),
	})
}

func warningReason(reason eventsv1.CourierWarningReason) (string, error) {
	switch reason {
	case eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP:
		return "Courier cancelled after pickup", nil
	case eventsv1.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD:
		return "Delivery timed out with no payable reward", nil
	default:
		return "", fmt.Errorf("unsupported courier warning reason %s", reason)
	}
}

func RegisterCourierWarningConsumer(subscriber *kafka.Subscriber, consumer *CourierWarningConsumer) error {
	return kafka.Register(subscriber, CourierWarningRequestedTopic,
		func() *eventsv1.CourierWarningRequested { return new(eventsv1.CourierWarningRequested) },
		consumer.Handle)
}
