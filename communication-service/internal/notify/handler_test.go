package notify

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/email"
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

func (f *fakeSender) Send(
	_ context.Context,
	chatID int64,
	text string,
	_ *telegram.InlineKeyboard,
) (telegram.Message, error) {
	if err := f.errs[chatID]; err != nil {
		return telegram.Message{}, err
	}
	f.sent = append(f.sent, sent{chatID, text})
	return telegram.Message{MessageID: int64(100 + len(f.sent)), Chat: telegram.Chat{ID: chatID}}, nil
}

type fakeTargets map[[2]int64]uuid.UUID

func (f fakeTargets) SaveReplyTarget(_ context.Context, chatID, messageID int64, orderID uuid.UUID, _ time.Time) error {
	f[[2]int64{chatID, messageID}] = orderID
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

// fakeOrders holds each order's latest applied revision.
type fakeOrders struct {
	revisions map[string]int64
	courierID uuid.UUID
	err       error
}

func (f *fakeOrders) Revision(_ context.Context, id uuid.UUID) (int64, bool, error) {
	revision, ok := f.revisions[id.String()]
	return revision, ok, f.err
}

func (f *fakeOrders) IsCourier(_ context.Context, _, userID uuid.UUID) (bool, error) {
	return f.courierID == userID, f.err
}

type fakeDeliveries map[[2]uuid.UUID]bool

func (f fakeDeliveries) HasBeenAccepted(_ context.Context, eventID, userID uuid.UUID) (bool, error) {
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
	h := &Handler{Sender: sender, Links: links, Deliveries: deliveries, Orders: &fakeOrders{},
		Since: now.Add(-30 * 24 * time.Hour),
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
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
	e := event(
		eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
		user(courierID),
	)
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
		{
			"blocked",
			&telegram.APIError{Code: http.StatusForbidden, Description: "Forbidden: bot was blocked by the user"},
			false,
			true,
		},
		{
			"chat not found",
			&telegram.APIError{Code: http.StatusBadRequest, Description: "Bad Request: chat not found"},
			false,
			true,
		},
		{
			"other rejection",
			&telegram.APIError{Code: http.StatusBadRequest, Description: "Bad Request: message is too long"},
			false,
			false,
		},
		{"rate limited", &telegram.APIError{Code: http.StatusTooManyRequests, RetryAfter: time.Second}, true, false},
		{"server error", &telegram.APIError{Code: http.StatusBadGateway}, true, false},
		{"network", errors.New("dial tcp: timeout"), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sender, links, deliveries := newHandler()
			sender.errs[1] = tc.err
			e := event(
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
				eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
				user(courierID),
			)
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
		"late event": func(e *eventsv1.OrderStatusChanged, _ *fakeLinks) {
			e.OccurredAt = timestamppb.New(now.Add(-LateWindow - time.Second))
		},
		"malformed event ID":  func(e *eventsv1.OrderStatusChanged, _ *fakeLinks) { e.EventId = "not-a-uuid" },
		"malformed recipient": func(e *eventsv1.OrderStatusChanged, _ *fakeLinks) { e.RequesterId = "not-a-uuid" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			h, sender, links, _ := newHandler()
			e := event(
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
				eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
				user(courierID),
			)
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

func TestHandleMakesNotificationsRepliable(t *testing.T) {
	h, _, _, _ := newHandler()
	targets := fakeTargets{}
	h.ReplyTargets = targets
	e := event(
		eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
		user(courierID),
	)
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if got := targets[[2]int64{1, 101}]; got.String() != e.GetOrderId() {
		t.Fatalf("reply targets = %v", targets)
	}
}

func TestHandleSendsOutcomesHoweverLate(t *testing.T) {
	h, sender, _, _ := newHandler()
	e := event(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, eventsv1.OrderStatus_ORDER_STATUS_FAILED, system)
	e.OccurredAt = timestamppb.New(now.Add(-6 * 24 * time.Hour))
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 2 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestHandleSkipsEventsBeforeNotificationsStarted(t *testing.T) {
	h, sender, _, _ := newHandler()
	h.Since = now
	e := event(eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, eventsv1.OrderStatus_ORDER_STATUS_FAILED, system)
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestHandleSendsAcceptanceOnlyWhileCurrent(t *testing.T) {
	cases := []struct {
		name      string
		revisions func(orderID string) map[string]int64
		wantSent  bool
	}{
		{"current", func(id string) map[string]int64 { return map[string]int64{id: 4} }, true},
		{"superseded", func(id string) map[string]int64 { return map[string]int64{id: 5} }, false},
		{"order unknown", func(string) map[string]int64 { return nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sender, _, _ := newHandler()
			e := event(
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
				user(requesterID),
			)
			// Still-current notifications have no late window.
			e.OccurredAt = timestamppb.New(now.Add(-2 * time.Hour))
			h.Orders = &fakeOrders{revisions: tc.revisions(e.GetOrderId())}
			if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
				t.Fatal(err)
			}
			if (len(sender.sent) == 1) != tc.wantSent || (tc.wantSent && sender.sent[0].chatID != 2) {
				t.Fatalf("sent = %+v", sender.sent)
			}
		})
	}
}

// An unknown order must not receive an acceptance notice, regardless of age.
func TestHandleSkipsAcceptanceForUnknownOrder(t *testing.T) {
	cases := []struct {
		name     string
		age      time.Duration
		wantSent bool
	}{
		{"fresh", time.Minute, false},
		{"old", LateWindow + time.Minute, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, sender, _, _ := newHandler()
			e := event(
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
				user(requesterID),
			)
			e.OccurredAt = timestamppb.New(now.Add(-tc.age))
			h.Orders = &fakeOrders{}
			if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
				t.Fatal(err)
			}
			if (len(sender.sent) == 1) != tc.wantSent {
				t.Fatalf("sent = %+v", sender.sent)
			}
		})
	}
}

func TestHandleReplaysWhenRevisionUnreadable(t *testing.T) {
	h, sender, _, _ := newHandler()
	h.Orders = &fakeOrders{err: errors.New("connection refused")}
	e := event(
		eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
		eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		user(requesterID),
	)
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err == nil {
		t.Fatal("revision failure was not returned for replay")
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

type fakeMailer struct {
	sent []email.Email
	err  error
}

func (f *fakeMailer) Send(_ context.Context, e email.Email) error {
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, e)
	return nil
}

type queuedEmail struct {
	eventID  uuid.UUID
	userID   uuid.UUID
	text     string
	validity EmailValidity
}

type fakeEmailQueue struct {
	queued []queuedEmail
	err    error
}

func (f *fakeEmailQueue) Enqueue(
	_ context.Context,
	eventID, userID uuid.UUID,
	text string,
	_ time.Time,
	validity EmailValidity,
) error {
	if f.err != nil {
		return f.err
	}
	for _, queued := range f.queued {
		if queued.eventID == eventID && queued.userID == userID {
			return nil
		}
	}
	f.queued = append(f.queued, queuedEmail{eventID, userID, text, validity})
	return nil
}

// withEmail enables durable fallback with the requester unlinked from Telegram.
func withEmail(h *Handler, links *fakeLinks) *fakeEmailQueue {
	queue := &fakeEmailQueue{}
	h.EmailQueue = queue
	delete(links.chats, requesterID)
	return queue
}

func outcome() *eventsv1.OrderStatusChanged {
	return event(eventsv1.OrderStatus_ORDER_STATUS_DELIVERED, eventsv1.OrderStatus_ORDER_STATUS_COMPLETED, system)
}

func TestHandleQueuesOutcomesWithoutNeedingTheUserEmail(t *testing.T) {
	h, sender, links, deliveries := newHandler()
	queue := withEmail(h, links)
	for range 2 {
		if err := h.Handle(context.Background(), kafka.Metadata{}, outcome()); err != nil {
			t.Fatal(err)
		}
	}
	if len(queue.queued) != 1 || queue.queued[0].eventID != eventID || queue.queued[0].userID != requesterID ||
		queue.queued[0].text != "Your order from Fine Food is complete." {
		t.Fatalf("queued = %+v", queue.queued)
	}
	if len(sender.sent) != 1 || sender.sent[0].chatID != 2 {
		t.Fatalf("telegram = %+v", sender.sent)
	}
	if deliveries[[2]uuid.UUID{eventID, requesterID}] {
		t.Fatal("an unsent email was recorded as sent")
	}
}

func TestHandleDoesNotEmailProgressUpdates(t *testing.T) {
	h, _, links, _ := newHandler()
	queue := withEmail(h, links)
	e := event(
		eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
		user(courierID),
	)
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil || len(queue.queued) != 0 {
		t.Fatalf("err=%v queued=%+v", err, queue.queued)
	}
}

func TestHandleQueuesEveryoneWhileTelegramIsDisabled(t *testing.T) {
	h, _, links, _ := newHandler()
	queue := withEmail(h, links)
	h.Sender = nil
	if err := h.Handle(context.Background(), kafka.Metadata{}, outcome()); err != nil {
		t.Fatal(err)
	}
	if len(queue.queued) != 2 {
		t.Fatalf("queued = %+v", queue.queued)
	}
}

func TestHandleQueuesEmailWhenTheTelegramChatIsGone(t *testing.T) {
	h, sender, links, _ := newHandler()
	queue := withEmail(h, links)
	sender.errs[2] = &telegram.APIError{
		Code:        http.StatusForbidden,
		Description: "Forbidden: bot was blocked by the user",
	}
	if err := h.Handle(context.Background(), kafka.Metadata{}, outcome()); err != nil {
		t.Fatal(err)
	}
	if len(links.unlinked) != 1 || len(queue.queued) != 2 || queue.queued[1].userID != courierID {
		t.Fatalf("unlinked=%v queued=%+v", links.unlinked, queue.queued)
	}
}

func TestHandleReplaysWhenEmailCannotBePersisted(t *testing.T) {
	h, sender, links, deliveries := newHandler()
	queue := withEmail(h, links)
	queue.err = errors.New("database unavailable")
	if err := h.Handle(context.Background(), kafka.Metadata{}, outcome()); err == nil {
		t.Fatal("email was acknowledged without being persisted")
	}
	if len(queue.queued) != 0 || len(sender.sent) != 0 || len(deliveries) != 0 {
		t.Fatalf("queued=%+v sent=%+v deliveries=%+v", queue.queued, sender.sent, deliveries)
	}
}

func offerEvent(age time.Duration) *eventsv1.OrderOfferMade {
	return &eventsv1.OrderOfferMade{
		EventId: eventID.String(), OrderId: uuid.NewString(), Revision: 5, OfferId: uuid.NewString(),
		CourierId: courierID.String(), RequesterId: requesterID.String(), Credits: 15,
		PickupLocationName: "Fine Food", OccurredAt: timestamppb.New(now.Add(-age)),
	}
}

func TestHandleOfferTellsTheRequester(t *testing.T) {
	h, sender, _, deliveries := newHandler()
	if err := h.HandleOffer(context.Background(), kafka.Metadata{}, offerEvent(time.Minute)); err != nil {
		t.Fatal(err)
	}
	want := sent{1, "A courier offered 15 credits to deliver your order from Fine Food. " +
		"If the offer is still open, you can accept it in the FoC app."}
	if len(sender.sent) != 1 || sender.sent[0] != want || !deliveries[[2]uuid.UUID{eventID, requesterID}] {
		t.Fatalf("sent = %+v", sender.sent)
	}
	// A replay doesn't send it again.
	if err := h.HandleOffer(context.Background(), kafka.Metadata{}, offerEvent(time.Minute)); err != nil ||
		len(sender.sent) != 1 {
		t.Fatalf("replay: err=%v sent=%+v", err, sender.sent)
	}
}

func TestHandleOfferEmailsRequestersWithoutTelegram(t *testing.T) {
	h, sender, links, _ := newHandler()
	queue := withEmail(h, links)
	if err := h.HandleOffer(context.Background(), kafka.Metadata{}, offerEvent(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 || len(queue.queued) != 1 || queue.queued[0].userID != requesterID {
		t.Fatalf("telegram=%+v email=%+v", sender.sent, queue.queued)
	}
}

func TestHandleOfferSkips(t *testing.T) {
	for name, tc := range map[string]struct {
		event *eventsv1.OrderOfferMade
		since time.Time
	}{
		"late":                     {offerEvent(LateWindow + time.Second), time.Time{}},
		"before notifications ran": {offerEvent(time.Minute), now},
		"malformed requester":      {func() *eventsv1.OrderOfferMade { e := offerEvent(time.Minute); e.RequesterId = "x"; return e }(), time.Time{}},
	} {
		h, sender, _, _ := newHandler()
		if !tc.since.IsZero() {
			h.Since = tc.since
		}
		if err := h.HandleOffer(context.Background(), kafka.Metadata{}, tc.event); err != nil || len(sender.sent) != 0 {
			t.Fatalf("%s: err=%v sent=%+v", name, err, sender.sent)
		}
	}
}

func TestHandleEmailsTheRemovedCourierHoweverLate(t *testing.T) {
	h, sender, links, _ := newHandler()
	queue := withEmail(h, links)
	delete(links.chats, courierID)
	e := event(eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
		&eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_ADMIN, UserId: proto.String(uuid.NewString())})
	e.CourierId, e.PreviousCourierId = nil, proto.String(courierID.String())
	e.OccurredAt = timestamppb.New(now.Add(-3 * time.Hour))
	// The order was reassigned since; the removed courier must still hear.
	h.Orders = &fakeOrders{revisions: map[string]int64{e.GetOrderId(): 9}}
	if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 0 || len(queue.queued) != 1 || queue.queued[0].userID != courierID {
		t.Fatalf("telegram=%+v email=%+v", sender.sent, queue.queued)
	}
}

func TestUnassignmentSkipsReturningCourier(t *testing.T) {
	for _, email := range []bool{false, true} {
		name := "Telegram"
		if email {
			name = "email"
		}
		t.Run(name, func(t *testing.T) {
			h, sender, links, _ := newHandler()
			queue := withEmail(h, links)
			if !email {
				links.chats[courierID] = 2
			}
			e := event(
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
				eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
				user(requesterID),
			)
			e.CourierId, e.PreviousCourierId = nil, proto.String(courierID.String())
			h.Orders = &fakeOrders{courierID: courierID}
			if err := h.Handle(context.Background(), kafka.Metadata{}, e); err != nil {
				t.Fatal(err)
			}
			if len(sender.sent) != 0 || len(queue.queued) != 0 {
				t.Fatalf("sent stale unassignment: telegram=%v email=%v", sender.sent, queue.queued)
			}
		})
	}
}
