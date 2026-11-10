package warning

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type logicStub struct {
	err    error
	calls  int
	record CourierWarningRequest
}

func (s *logicStub) RecordCourierWarning(_ context.Context, request CourierWarningRequest) error {
	s.calls++
	s.record = request
	return s.err
}

func warningEvent(
	reason eventsv1.CourierWarningReason,
	occurredAt *timestamppb.Timestamp,
) *eventsv1.CourierWarningRequested {
	return &eventsv1.CourierWarningRequested{
		EventId: uuid.NewString(), OrderId: uuid.NewString(), CourierId: uuid.NewString(),
		Reason: reason, OccurredAt: occurredAt,
	}
}

// Events that can never succeed are acknowledged without reaching the logic.
func TestCourierWarningConsumerSkipsUnprocessableEvents(t *testing.T) {
	for name, event := range map[string]*eventsv1.CourierWarningRequested{
		"nil event":          nil,
		"unsupported reason": warningEvent(eventsv1.CourierWarningReason_COURIER_WARNING_REASON_UNSPECIFIED, timestamppb.Now()),
		"invalid occurrence": warningEvent(
			eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP,
			&timestamppb.Timestamp{Seconds: 1, Nanos: -1},
		),
	} {
		t.Run(name, func(t *testing.T) {
			logic := &logicStub{}
			consumer := &CourierWarningConsumer{Logic: logic}
			if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
				t.Fatalf("%s: got %v, want acknowledged without error", name, err)
			}
			if logic.calls != 0 {
				t.Fatalf("%s: recorded warnings = %d, want 0", name, logic.calls)
			}
		})
	}
}

// Rejections the logic recognises as permanent are acknowledged, not retried.
func TestCourierWarningConsumerAcknowledgesKnownRejections(t *testing.T) {
	for name, logicErr := range map[string]error{
		"invalid fields":  fmt.Errorf("record courier warning: %w", ErrInvalidRequest),
		"unknown courier": fmt.Errorf("record courier warning: %w", ErrUnknownCourier),
	} {
		t.Run(name, func(t *testing.T) {
			logic := &logicStub{err: logicErr}
			consumer := &CourierWarningConsumer{Logic: logic}
			event := warningEvent(
				eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP,
				timestamppb.New(time.Date(2026, time.October, 12, 10, 30, 0, 0, time.UTC)),
			)
			if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
				t.Fatalf("%s: got %v, want acknowledged without error", name, err)
			}
			if logic.calls != 1 {
				t.Fatalf("%s: recorded warnings = %d, want 1", name, logic.calls)
			}
		})
	}
}

// Any other failure is returned so the subscriber stops and redelivers.
func TestCourierWarningConsumerReturnsTransientError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	logic := &logicStub{err: wantErr}
	consumer := &CourierWarningConsumer{Logic: logic}
	event := warningEvent(
		eventsv1.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD,
		timestamppb.Now(),
	)
	if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); !errors.Is(err, wantErr) {
		t.Fatalf("transient error = %v, want %v", err, wantErr)
	}
	if logic.calls != 1 {
		t.Fatalf("recorded warnings = %d, want 1", logic.calls)
	}
	if logic.record.Reason != "Delivery timed out with no payable reward" ||
		logic.record.EventID != event.EventId || logic.record.RequestID != event.OrderId ||
		logic.record.CourierID != event.CourierId {
		t.Fatalf("consumer mapped request = %+v for event %+v", logic.record, event)
	}
}
