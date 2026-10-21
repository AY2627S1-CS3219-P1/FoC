package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	now         = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	requesterID = uuid.MustParse("c55ce32c-2af7-46d1-a088-6a976d3bdbaf")
	courierID   = uuid.MustParse("41dbf670-f32c-4cca-9473-5861c274e789")
	eventID     = uuid.MustParse("37dbf670-f32c-4cca-9473-5861c274e789")
)

type sent struct {
	chatID int64
	text   string
}

type fakeSender struct {
	sent []sent
	errs map[int64]error
}

func (f *fakeSender) SendMessage(_ context.Context, chatID int64, text string) error {
	if err := f.errs[chatID]; err != nil {
		return err
	}
	f.sent = append(f.sent, sent{chatID, text})
	return nil
}

type fakeLinks struct {
	chats    map[uuid.UUID]int64
	unlinked []int64
}

func (f *fakeLinks) Get(_ context.Context, userID uuid.UUID) (linking.Link, error) {
	chat, ok := f.chats[userID]
	if !ok {
		return linking.Link{}, linking.ErrNotLinked
	}
	return linking.Link{UserID: userID, ChatID: chat}, nil
}

func (f *fakeLinks) UnlinkChat(_ context.Context, chatID int64) error {
	f.unlinked = append(f.unlinked, chatID)
	return nil
}

type fakeDeliveries map[[2]uuid.UUID]bool

func (f fakeDeliveries) HasBeenSent(_ context.Context, eventID, userID uuid.UUID) (bool, error) {
	return f[[2]uuid.UUID{eventID, userID}], nil
}

func (f fakeDeliveries) RecordSent(_ context.Context, eventID, userID uuid.UUID, _ time.Time) error {
	f[[2]uuid.UUID{eventID, userID}] = true
	return nil
}

func newHandler() (*Handler, *fakeSender, *fakeLinks, fakeDeliveries) {
	sender := &fakeSender{errs: map[int64]error{}}
	links := &fakeLinks{chats: map[uuid.UUID]int64{requesterID: 1, courierID: 2}}
	deliveries := fakeDeliveries{}
	h := &Handler{Sender: sender, Links: links, Deliveries: deliveries,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	return h, sender, links, deliveries
}

func event(previous, status eventsv1.OrderStatus, actor *eventsv1.Actor) *eventsv1.OrderStatusChanged {
	return &eventsv1.OrderStatusChanged{
		EventId: eventID.String(), OrderId: uuid.NewString(), Revision: 4,
		PreviousStatus: previous, Status: status, Actor: actor,
		RequesterId: requesterID.String(), CourierId: proto.String(courierID.String()),
		PickupLocationName: "Fine Food", OccurredAt: timestamppb.New(now.Add(-time.Minute)),
	}
}

func user(id uuid.UUID) *eventsv1.Actor {
	return &eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_USER, UserId: proto.String(id.String())}
}

var system = &eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_SYSTEM}

func TestHandleNotifiesTheOtherParticipant(t *testing.T) {
	h, sender, _, _ := newHandler()
	e := event(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, user(courierID))
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	want := sent{1, "Your order from Fine Food has been picked up and is on its way."}
	if len(sender.sent) != 1 || sender.sent[0] != want {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestHandleNotifiesBothParticipantsOfSystemChanges(t *testing.T) {
	h, sender, _, _ := newHandler()
	e := event(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, eventsv1.OrderStatus_ORDER_STATUS_FAILED, system)
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 || sender.sent[0].chatID != 1 || sender.sent[1].chatID != 2 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestHandleSkipsRecipientsAlreadySentAfterReplay(t *testing.T) {
	h, sender, _, _ := newHandler()
	e := event(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, eventsv1.OrderStatus_ORDER_STATUS_FAILED, system)
	// The courier's send fails transiently after the requester's succeeded.
	sender.errs[2] = errors.New("connection reset")
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err == nil {
		t.Fatal("transient failure was not returned for replay")
	}
	delete(sender.errs, 2)
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 || sender.sent[0].chatID != 1 || sender.sent[1].chatID != 2 {
		t.Fatalf("replay resent the requester: %+v", sender.sent)
	}
}

func TestHandleSendFailures(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantErr    bool
		wantUnlink bool
	}{
		{"blocked", &telegram.APIError{Code: http.StatusForbidden, Description: "Forbidden: bot was blocked by the user"}, false, true},
		{"chat not found", &telegram.APIError{Code: http.StatusBadRequest, Description: "Bad Request: chat not found"}, false, true},
		{"other rejection", &telegram.APIError{Code: http.StatusBadRequest, Description: "Bad Request: message is too long"}, false, false},
		{"rate limited", &telegram.APIError{Code: http.StatusTooManyRequests, RetryAfter: time.Second}, true, false},
		{"server error", &telegram.APIError{Code: http.StatusBadGateway}, true, false},
		{"network", errors.New("dial tcp: timeout"), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sender, links, deliveries := newHandler()
			sender.errs[1] = tc.err
			e := event(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, user(courierID))
			err := h.Handle(context.Background(), kafka.Metadata{}, e)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if (len(links.unlinked) == 1) != tc.wantUnlink {
				t.Fatalf("unlinked = %v", links.unlinked)
			}
			if len(deliveries) != 0 {
				t.Fatal("a failed send was recorded as sent")
			}
		})
	}
}

func TestHandleSkipsWithoutSending(t *testing.T) {
	cases := map[string]func(*eventsv1.OrderStatusChanged, *fakeLinks){
		"unlinked recipient": func(_ *eventsv1.OrderStatusChanged, l *fakeLinks) { delete(l.chats, requesterID) },
		"stale event": func(e *eventsv1.OrderStatusChanged, _ *fakeLinks) {
			e.OccurredAt = timestamppb.New(now.Add(-MaxAge - time.Second))
		},
		"malformed event ID":  func(e *eventsv1.OrderStatusChanged, _ *fakeLinks) { e.EventId = "not-a-uuid" },
		"malformed recipient": func(e *eventsv1.OrderStatusChanged, _ *fakeLinks) { e.RequesterId = "not-a-uuid" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, sender, links, _ := newHandler()
			e := event(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, user(courierID))
			mutate(e, links)
			if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
				t.Fatal(err)
			}
			if len(sender.sent) != 0 {
				t.Fatalf("sent = %+v", sender.sent)
			}
		})
	}
}
