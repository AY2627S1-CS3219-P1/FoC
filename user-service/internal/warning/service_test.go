package warning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type repositoryStub struct {
	created []Warning
	list    []Warning
	err     error
}

func (r *repositoryStub) Create(_ context.Context, warning Warning) (bool, error) {
	r.created = append(r.created, warning)
	return true, r.err
}

func (r *repositoryStub) ListByUser(context.Context, uuid.UUID) ([]Warning, error) {
	return r.list, r.err
}

func TestRecordCourierWarning(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	eventID, courierID, requestID := uuid.New(), uuid.New(), uuid.New()
	occurredAt := time.Date(2026, time.October, 12, 10, 30, 0, 0, time.FixedZone("SGT", 8*60*60))
	err := service.RecordCourierWarning(context.Background(), CourierWarningRequest{
		EventID: eventID.String(), CourierID: courierID.String(), RequestID: requestID.String(), Reason: "  late cancellation  ",
		OccurredAt: occurredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.created) != 1 {
		t.Fatalf("created warnings = %d, want 1", len(repository.created))
	}
	got := repository.created[0]
	if got.UserID != courierID || got.RequestID != requestID || got.Reason != "late cancellation" || got.Status != StatusActive ||
		got.SourceEventID == nil || *got.SourceEventID != eventID || !got.CreatedAt.Equal(occurredAt) || got.CreatedAt.Location() != time.UTC {
		t.Fatalf("created warning = %+v", got)
	}
}

func TestRecordCourierWarningRejectsInvalidInput(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	occurredAt := time.Now().UTC()
	for _, request := range []CourierWarningRequest{
		{EventID: "bad", CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: "late", OccurredAt: occurredAt},
		{EventID: uuid.NewString(), CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: "  ", OccurredAt: occurredAt},
		{EventID: uuid.NewString(), CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: strings.Repeat("x", 2001), OccurredAt: occurredAt},
		{EventID: uuid.NewString(), CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: "late"},
	} {
		if err := service.RecordCourierWarning(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %+v: got %v, want invalid request", request, err)
		}
	}
	if len(repository.created) != 0 {
		t.Fatalf("persisted warnings = %d, want 0", len(repository.created))
	}
}

func TestCourierWarningConsumerMapsEvent(t *testing.T) {
	for _, tc := range []struct {
		reason     eventsv1.CourierWarningReason
		wantReason string
	}{
		{eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP, "Courier cancelled after pickup"},
		{eventsv1.CourierWarningReason_COURIER_WARNING_REASON_ZERO_PAYABLE_REWARD, "Delivery timed out with no payable reward"},
	} {
		t.Run(tc.reason.String(), func(t *testing.T) {
			repository := &repositoryStub{}
			consumer := &CourierWarningConsumer{Logic: NewService(repository)}
			occurredAt := time.Date(2026, time.October, 12, 10, 30, 0, 0, time.UTC)
			event := &eventsv1.CourierWarningRequested{
				EventId:    uuid.NewString(),
				OrderId:    uuid.NewString(),
				CourierId:  uuid.NewString(),
				Reason:     tc.reason,
				OccurredAt: timestamppb.New(occurredAt),
			}

			if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
				t.Fatal(err)
			}
			if len(repository.created) != 1 || repository.created[0].Reason != tc.wantReason ||
				repository.created[0].RequestID.String() != event.OrderId || repository.created[0].UserID.String() != event.CourierId ||
				!repository.created[0].CreatedAt.Equal(occurredAt) {
				t.Fatalf("consumer persisted %+v for event %+v", repository.created, event)
			}
		})
	}
}

func TestCourierWarningConsumerRejectsInvalidTimestamp(t *testing.T) {
	for name, timestamp := range map[string]*timestamppb.Timestamp{
		"missing":              nil,
		"invalid nanoseconds":  {Seconds: 1, Nanos: -1},
		"out of range seconds": {Seconds: 253402300800},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &repositoryStub{}
			consumer := &CourierWarningConsumer{Logic: NewService(repository)}
			event := &eventsv1.CourierWarningRequested{
				EventId: uuid.NewString(), OrderId: uuid.NewString(), CourierId: uuid.NewString(),
				Reason:     eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP,
				OccurredAt: timestamp,
			}

			if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("timestamp %v: got %v, want invalid request", timestamp, err)
			}
			if len(repository.created) != 0 {
				t.Fatalf("persisted warnings = %d, want 0", len(repository.created))
			}
		})
	}
}

func TestCourierWarningConsumerRejectsUnspecifiedReason(t *testing.T) {
	repository := &repositoryStub{}
	consumer := &CourierWarningConsumer{Logic: NewService(repository)}
	event := &eventsv1.CourierWarningRequested{
		EventId: uuid.NewString(), OrderId: uuid.NewString(), CourierId: uuid.NewString(),
		Reason:     eventsv1.CourierWarningReason_COURIER_WARNING_REASON_UNSPECIFIED,
		OccurredAt: timestamppb.Now(),
	}
	if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err == nil {
		t.Fatal("expected unsupported reason error")
	}
	if len(repository.created) != 0 {
		t.Fatalf("persisted warnings = %d, want 0", len(repository.created))
	}
}

func TestListWarningsReturnsRepositoryError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	service := NewService(&repositoryStub{err: wantErr})
	if _, err := service.ListWarnings(context.Background(), uuid.New()); !errors.Is(err, wantErr) {
		t.Fatalf("list warning error = %v, want wrapped database error", err)
	}
}
