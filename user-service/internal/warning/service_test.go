package warning

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/pagination"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type repositoryStub struct {
	created      []Warning
	list         []Warning
	adminList    []AdminWarning
	total        int64
	listUserID   uuid.UUID
	listOffset   int64
	listLimit    int64
	removeID     uuid.UUID
	removedAt    time.Time
	removeReason *string
	appealID     *uuid.UUID
	actorID      *uuid.UUID
	removed      Warning
	err          error
	countErr     error
	removeErr    error
}

func (r *repositoryStub) Create(_ context.Context, warning Warning) (bool, error) {
	r.created = append(r.created, warning)
	return true, r.err
}

func (r *repositoryStub) ListByUser(
	_ context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]Warning, error) {
	r.listUserID = userID
	r.listOffset = offset
	r.listLimit = limit
	return r.list, r.err
}

func (r *repositoryStub) ListForAdmin(
	_ context.Context,
	userID uuid.UUID,
	offset, limit int64,
) ([]AdminWarning, error) {
	r.listUserID = userID
	r.listOffset = offset
	r.listLimit = limit
	return r.adminList, r.err
}

func (r *repositoryStub) CountByUser(context.Context, uuid.UUID) (int64, error) {
	return r.total, r.countErr
}

func (r *repositoryStub) Remove(
	_ context.Context,
	id uuid.UUID,
	removedAt time.Time,
	reason *string,
	appealID, actorID *uuid.UUID,
) (Warning, error) {
	r.removeID = id
	r.removedAt = removedAt
	r.removeReason = reason
	r.appealID = appealID
	r.actorID = actorID
	return r.removed, r.removeErr
}

func TestRecordCourierWarning(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	eventID, courierID, requestID := uuid.New(), uuid.New(), uuid.New()
	occurredAt := time.Date(2026, time.October, 12, 10, 30, 0, 0, time.FixedZone("SGT", 8*60*60))
	err := service.RecordCourierWarning(context.Background(), CourierWarningRequest{
		EventID:    eventID.String(),
		CourierID:  courierID.String(),
		RequestID:  requestID.String(),
		Reason:     "  late cancellation  ",
		OccurredAt: occurredAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.created) != 1 {
		t.Fatalf("created warnings = %d, want 1", len(repository.created))
	}
	got := repository.created[0]
	if got.UserID != courierID || got.RequestID != requestID || got.Reason != "late cancellation" ||
		got.Status != StatusActive ||
		got.SourceEventID == nil ||
		*got.SourceEventID != eventID ||
		!got.CreatedAt.Equal(occurredAt) ||
		got.CreatedAt.Location() != time.UTC {
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

func TestCourierWarningConsumerSkipsInvalidTimestamp(t *testing.T) {
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

			if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
				t.Fatalf("timestamp %v: got %v, want acknowledged without error", timestamp, err)
			}
			if len(repository.created) != 0 {
				t.Fatalf("persisted warnings = %d, want 0", len(repository.created))
			}
		})
	}
}

func TestCourierWarningConsumerSkipsUnspecifiedReason(t *testing.T) {
	repository := &repositoryStub{}
	consumer := &CourierWarningConsumer{Logic: NewService(repository)}
	event := &eventsv1.CourierWarningRequested{
		EventId: uuid.NewString(), OrderId: uuid.NewString(), CourierId: uuid.NewString(),
		Reason:     eventsv1.CourierWarningReason_COURIER_WARNING_REASON_UNSPECIFIED,
		OccurredAt: timestamppb.Now(),
	}
	if err := consumer.Handle(context.Background(), kafka.Metadata{}, event); err != nil {
		t.Fatalf("unspecified reason: got %v, want acknowledged without error", err)
	}
	if len(repository.created) != 0 {
		t.Fatalf("persisted warnings = %d, want 0", len(repository.created))
	}
}

func TestListWarningsReturnsRepositoryError(t *testing.T) {
	wantErr := errors.New("database unavailable")
	service := NewService(&repositoryStub{err: wantErr})
	page, _ := pagination.New(1, 20)
	if _, err := service.ListWarnings(context.Background(), uuid.New(), page); !errors.Is(err, wantErr) {
		t.Fatalf("list warning error = %v, want wrapped database error", err)
	}
}

func TestListWarningsUsesOffsetAndReturnsPageInfo(t *testing.T) {
	userID := uuid.New()
	warnings := []Warning{{ID: uuid.New()}, {ID: uuid.New()}}
	repository := &repositoryStub{list: warnings, total: 7}
	service := NewService(repository)
	// Page 3 with size 2 gives offset 4, so the assertion cannot pass by the
	// page number and size coincidentally equaling the offset.
	page, err := pagination.New(3, 2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := service.ListWarnings(context.Background(), userID, page)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 2 || got.Warnings[0].ID != warnings[0].ID ||
		got.PageInfo.Page() != 3 || got.PageInfo.PageSize() != 2 ||
		got.PageInfo.TotalItems() != 7 || got.PageInfo.TotalPages() != 4 {
		t.Fatalf("warning page = %+v", got)
	}
	if repository.listUserID != userID || repository.listOffset != 4 || repository.listLimit != 2 {
		t.Fatalf(
			"repository page = user %s offset %d limit %d",
			repository.listUserID,
			repository.listOffset,
			repository.listLimit,
		)
	}
}

func TestListWarningsRejectsInvalidUserWithoutTouchingTheRepository(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	page, _ := pagination.New(1, 20)
	_, err := service.ListWarnings(context.Background(), uuid.Nil, page)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid user error = %v, want ErrInvalidRequest", err)
	}
	if repository.listUserID != uuid.Nil {
		t.Fatalf("repository reached with user %s", repository.listUserID)
	}
}

func TestListWarningsMapsCountErrors(t *testing.T) {
	wantErr := errors.New("count unavailable")
	service := NewService(&repositoryStub{countErr: wantErr})
	page, _ := pagination.New(1, 20)
	if _, err := service.ListWarnings(context.Background(), uuid.New(), page); !errors.Is(err, wantErr) {
		t.Fatalf("count error = %v, want wrapped database error", err)
	}
}

func TestListWarningsForAdminReturnsReadProjectionPage(t *testing.T) {
	userID := uuid.New()
	updaterName := "Admin"
	adminWarnings := []AdminWarning{{
		Warning:       Warning{ID: uuid.New()},
		UpdatedByName: &updaterName,
	}}
	repository := &repositoryStub{adminList: adminWarnings, total: 7}
	service := NewService(repository)
	// Page 3 with size 2 gives offset 4, so the assertion cannot pass by the
	// page number and size coincidentally equaling the offset.
	page, err := pagination.New(3, 2)
	if err != nil {
		t.Fatal(err)
	}

	got, err := service.ListWarningsForAdmin(context.Background(), userID, page)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Warnings) != 1 || got.Warnings[0].ID != adminWarnings[0].ID ||
		got.Warnings[0].UpdatedByName == nil || *got.Warnings[0].UpdatedByName != updaterName ||
		got.PageInfo.Page() != 3 || got.PageInfo.PageSize() != 2 ||
		got.PageInfo.TotalItems() != 7 || got.PageInfo.TotalPages() != 4 {
		t.Fatalf("admin warning page = %+v", got)
	}
	if repository.listUserID != userID || repository.listOffset != 4 || repository.listLimit != 2 {
		t.Fatalf(
			"repository page = user %s offset %d limit %d",
			repository.listUserID,
			repository.listOffset,
			repository.listLimit,
		)
	}
}

func TestListWarningsForAdminRejectsInvalidUserWithoutTouchingTheRepository(t *testing.T) {
	repository := &repositoryStub{}
	service := NewService(repository)
	page, _ := pagination.New(1, 20)
	_, err := service.ListWarningsForAdmin(context.Background(), uuid.Nil, page)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("invalid user error = %v, want ErrInvalidRequest", err)
	}
	if repository.listUserID != uuid.Nil {
		t.Fatalf("repository reached with user %s", repository.listUserID)
	}
}

func TestListWarningsForAdminMapsRepositoryAndCountErrors(t *testing.T) {
	wantErr := errors.New("database unavailable")
	page, _ := pagination.New(1, 20)
	if _, err := NewService(&repositoryStub{err: wantErr}).
		ListWarningsForAdmin(context.Background(), uuid.New(), page); !errors.Is(err, wantErr) {
		t.Fatalf("admin list error = %v, want wrapped database error", err)
	}
	if _, err := NewService(&repositoryStub{countErr: wantErr}).
		ListWarningsForAdmin(context.Background(), uuid.New(), page); !errors.Is(err, wantErr) {
		t.Fatalf("admin count error = %v, want wrapped database error", err)
	}
}

func TestMarkWarningAsRemovedPassesAuditMetadata(t *testing.T) {
	warningID, actorID, appealID := uuid.New(), uuid.New(), uuid.New()
	reason := "appeal overturned"
	repository := &repositoryStub{}
	service := NewService(repository)

	if err := service.MarkWarningAsRemoved(context.Background(), warningID, &reason, &appealID, actorID); err != nil {
		t.Fatal(err)
	}
	if repository.removeID != warningID || repository.removeReason == nil || *repository.removeReason != reason ||
		repository.appealID == nil || *repository.appealID != appealID ||
		repository.actorID == nil || *repository.actorID != actorID || repository.removedAt.IsZero() ||
		repository.removedAt.Location() != time.UTC {
		t.Fatalf("remove arguments: id=%s at=%s reason=%v appeal=%v actor=%v", repository.removeID,
			repository.removedAt, repository.removeReason, repository.appealID, repository.actorID)
	}
}

func TestMarkWarningAsRemovedValidatesAndMapsRepositoryErrors(t *testing.T) {
	actorID, warningID := uuid.New(), uuid.New()
	service := NewService(&repositoryStub{removeErr: ErrNotFound})
	if err := service.MarkWarningAsRemoved(
		context.Background(),
		warningID,
		nil,
		nil,
		actorID,
	); !errors.Is(
		err,
		ErrNotFound,
	) {
		t.Fatalf("missing warning error = %v, want ErrNotFound", err)
	}

	for _, tc := range []struct {
		name        string
		warningID   uuid.UUID
		actorID     uuid.UUID
		removedText *string
	}{
		{name: "nil warning ID", warningID: uuid.Nil, actorID: actorID},
		{name: "nil actor ID", warningID: warningID, actorID: uuid.Nil},
		{name: "reason too long", warningID: warningID, actorID: actorID, removedText: stringPointer(strings.Repeat("界", 2001))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repository := &repositoryStub{}
			service := NewService(repository)
			if err := service.MarkWarningAsRemoved(
				context.Background(),
				tc.warningID,
				tc.removedText,
				nil,
				tc.actorID,
			); err == nil {
				t.Fatal("invalid removal input accepted")
			}
			if !repository.removedAt.IsZero() {
				t.Fatal("invalid removal reached repository")
			}
		})
	}
}
