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

func (*repositoryStub) GetByEventID(context.Context, uuid.UUID) (Warning, error) {
	return Warning{}, nil
}
func (*repositoryStub) GetByID(context.Context, uuid.UUID) (Warning, error) { return Warning{}, nil }
func (r *repositoryStub) ListByUser(context.Context, uuid.UUID) ([]Warning, error) {
	return r.list, r.err
}
func (*repositoryStub) Remove(context.Context, uuid.UUID, time.Time, *string, *uuid.UUID, *uuid.UUID) (Warning, error) {
	return Warning{}, nil
}

func TestRecordCourierWarning(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	eventID, courierID, requestID := uuid.New(), uuid.New(), uuid.New()
	err := service.RecordCourierWarning(context.Background(), CourierWarningRequest{
		EventID: eventID.String(), CourierID: courierID.String(), RequestID: requestID.String(), Reason: "  late cancellation  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.created) != 1 {
		t.Fatalf("created warnings = %d, want 1", len(repository.created))
	}
	got := repository.created[0]
	if got.UserID != courierID || got.RequestID != requestID || got.Reason != "late cancellation" || got.Status != StatusActive ||
		got.SourceEventID == nil || *got.SourceEventID != eventID {
		t.Fatalf("created warning = %+v", got)
	}
}

func TestRecordCourierWarningRejectsInvalidInput(t *testing.T) {
	service := NewService(&repositoryStub{})
	for _, request := range []CourierWarningRequest{
		{EventID: "bad", CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: "late"},
		{EventID: uuid.NewString(), CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: "  "},
		{EventID: uuid.NewString(), CourierID: uuid.NewString(), RequestID: uuid.NewString(), Reason: strings.Repeat("x", 2001)},
	} {
		if err := service.RecordCourierWarning(context.Background(), request); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("request %+v: got %v, want invalid request", request, err)
		}
	}
}

func TestCourierWarningConsumerMapsEvent(t *testing.T) {
	repository := &repositoryStub{}
	consumer := &CourierWarningConsumer{Logic: NewService(repository)}
	event := &eventsv1.CourierWarningRequested{
		EventId: uuid.NewString(), OrderId: uuid.NewString(), CourierId: uuid.NewString(),
		Reason: eventsv1.CourierWarningReason_COURIER_WARNING_REASON_CANCELLED_AFTER_PICKUP,
	}
	if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
		t.Fatal(err)
	}
	if len(repository.created) != 1 || repository.created[0].Reason != "Courier cancelled after pickup" ||
		repository.created[0].RequestID.String() != event.OrderId || repository.created[0].UserID.String() != event.CourierId {
		t.Fatalf("consumer persisted %+v for event %+v", repository.created, event)
	}
}

func TestCourierWarningConsumerRejectsUnspecifiedReason(t *testing.T) {
	repository := &repositoryStub{}
	consumer := &CourierWarningConsumer{Logic: NewService(repository)}
	event := &eventsv1.CourierWarningRequested{
		EventId: uuid.NewString(), OrderId: uuid.NewString(), CourierId: uuid.NewString(),
		Reason: eventsv1.CourierWarningReason_COURIER_WARNING_REASON_UNSPECIFIED,
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
