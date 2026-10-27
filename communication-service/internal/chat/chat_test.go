package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	now       = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	requester = uuid.MustParse("c55ce32c-2af7-46d1-a088-6a976d3bdbaf")
	courier   = uuid.MustParse("41dbf670-f32c-4cca-9473-5861c274e789")
	stranger  = uuid.MustParse("51dbf670-f32c-4cca-9473-5861c274e789")
)

const (
	requesterChat int64 = 1
	courierChat   int64 = 2
	strangerChat  int64 = 3
)

func order(status string) Order {
	return Order{
		ID:          uuid.New(),
		RequesterID: requester,
		CourierID:   courier,
		Status:      status,
		Revision:    3,
		PickupName:  "Fine Food",
	}
}

func TestIsOpen(t *testing.T) {
	ended := func(status string, ago time.Duration) Order {
		o := order(status)
		o.EndedAt = now.Add(-ago)
		return o
	}
	unassigned := order("awaiting_courier")
	unassigned.CourierID = uuid.Nil
	cases := map[string]struct {
		o    Order
		want bool
	}{
		"awaiting pickup":          {order(AwaitingPickup), true},
		"delivering":               {order(Delivering), true},
		"delivered":                {order(Delivered), true},
		"no courier":               {unassigned, false},
		"completed within grace":   {ended(Completed, GracePeriod-time.Minute), true},
		"cancelled after grace":    {ended(Cancelled, GracePeriod+time.Minute), false},
		"completed at grace limit": {ended(Completed, GracePeriod), false},
		"failed within grace":      {ended(Failed, time.Minute), true},
		"expired never had a chat": {ended("expired", time.Minute), false},
	}
	for name, tc := range cases {
		if got := tc.o.IsOpen(now); got != tc.want {
			t.Errorf("%s: IsOpen = %v", name, got)
		}
	}
}

type fakeOrders map[uuid.UUID]Order

func (f fakeOrders) SaveOrder(_ context.Context, o Order, _ time.Time) error {
	if stored, ok := f[o.ID]; ok && stored.Revision >= o.Revision {
		return nil
	}
	f[o.ID] = o
	return nil
}

func TestProjection(t *testing.T) {
	store := fakeOrders{}
	p := &Projection{
		Store: store,
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:   func() time.Time { return now },
	}
	id := uuid.New()
	e := &eventsv1.OrderStatusChanged{EventId: uuid.NewString(), OrderId: id.String(), Revision: 5,
		Status: eventsv1.OrderStatus_ORDER_STATUS_CANCELLED, RequesterId: requester.String(),
		CourierId: proto.String(courier.String()), PickupLocationName: "Fine Food", OccurredAt: timestamppb.New(now)}
	if err := p.Apply(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	got := store[id]
	if got.Status != Cancelled || got.CourierID != courier || !got.EndedAt.Equal(now) || got.Revision != 5 {
		t.Fatalf("stored = %+v", got)
	}

	if err := p.Apply(
		context.Background(),
		&eventsv1.OrderStatusChanged{OrderId: "bad", RequesterId: requester.String()},
	); err != nil {
		t.Fatalf("malformed event should be skipped: %v", err)
	}
}

// fakeStore implements Store in memory.
type fakeStore struct {
	orders     map[uuid.UUID]Order
	targets    map[[2]int64]uuid.UUID
	selections map[uuid.UUID]uuid.UUID
	messages   []Message
}

func newStore(orders ...Order) *fakeStore {
	s := &fakeStore{
		orders:     map[uuid.UUID]Order{},
		targets:    map[[2]int64]uuid.UUID{},
		selections: map[uuid.UUID]uuid.UUID{},
	}
	for _, o := range orders {
		s.orders[o.ID] = o
	}
	return s
}

func (s *fakeStore) GetOrder(_ context.Context, id uuid.UUID) (Order, error) {
	o, ok := s.orders[id]
	if !ok {
		return Order{}, ErrNotFound
	}
	return o, nil
}

func (s *fakeStore) UserOrders(_ context.Context, userID uuid.UUID, _ time.Time) ([]Order, error) {
	var out []Order
	for _, o := range s.orders {
		if o.RequesterID == userID || o.CourierID == userID {
			out = append(out, o)
		}
	}
	return out, nil
}

func (s *fakeStore) ReplyTarget(_ context.Context, chatID, messageID int64) (uuid.UUID, error) {
	id, ok := s.targets[[2]int64{chatID, messageID}]
	if !ok {
		return uuid.Nil, ErrNotFound
	}
	return id, nil
}

func (s *fakeStore) SaveReplyTarget(_ context.Context, chatID, messageID int64, orderID uuid.UUID, _ time.Time) error {
	s.targets[[2]int64{chatID, messageID}] = orderID
	return nil
}

func (s *fakeStore) Selection(_ context.Context, userID uuid.UUID) (uuid.UUID, error) {
	id, ok := s.selections[userID]
	if !ok {
		return uuid.Nil, ErrNotFound
	}
	return id, nil
}

func (s *fakeStore) Select(_ context.Context, userID, orderID uuid.UUID, _ time.Time) error {
	s.selections[userID] = orderID
	return nil
}

func (s *fakeStore) SaveMessage(_ context.Context, m Message) error {
	s.messages = append(s.messages, m)
	return nil
}

type sentMessage struct {
	chatID   int64
	text     string
	keyboard *telegram.InlineKeyboard
}

type fakeSender struct {
	sent     []sentMessage
	answered []string
	errs     map[int64]error
}

func (f *fakeSender) Send(
	_ context.Context,
	chatID int64,
	text string,
	kb *telegram.InlineKeyboard,
) (telegram.Message, error) {
	if err := f.errs[chatID]; err != nil {
		return telegram.Message{}, err
	}
	f.sent = append(f.sent, sentMessage{chatID, text, kb})
	return telegram.Message{MessageID: int64(100 + len(f.sent)), Chat: telegram.Chat{ID: chatID}}, nil
}

func (f *fakeSender) AnswerCallback(_ context.Context, _ string, text string) error {
	f.answered = append(f.answered, text)
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

func (f *fakeLinks) GetByChat(_ context.Context, chatID int64) (linking.Link, error) {
	for userID, chat := range f.chats {
		if chat == chatID {
			return linking.Link{UserID: userID, ChatID: chat}, nil
		}
	}
	return linking.Link{}, linking.ErrNotLinked
}

func (f *fakeLinks) UnlinkChat(_ context.Context, chatID int64) error {
	f.unlinked = append(f.unlinked, chatID)
	return nil
}

func newRelay(orders ...Order) (*Relay, *fakeSender, *fakeStore, *fakeLinks) {
	sender := &fakeSender{errs: map[int64]error{}}
	store := newStore(orders...)
	links := &fakeLinks{
		chats: map[uuid.UUID]int64{requester: requesterChat, courier: courierChat, stranger: strangerChat},
	}
	r := &Relay{Sender: sender, Links: links, Store: store,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
	return r, sender, store, links
}

func text(chatID int64, body string) *telegram.Message {
	return &telegram.Message{MessageID: 7, Chat: telegram.Chat{ID: chatID, Type: "private"}, Text: body}
}

func TestRelayToTheOnlyOpenOrder(t *testing.T) {
	o := order(Delivering)
	r, sender, store, _ := newRelay(o)
	if err := r.HandleMessage(context.Background(), text(courierChat, "I'm at the lobby")); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].chatID != requesterChat ||
		sender.sent[0].text != "Your courier (Fine Food order):\nI'm at the lobby" {
		t.Fatalf("sent = %+v", sender.sent)
	}
	if len(store.messages) != 1 || store.messages[0].Body != "I'm at the lobby" ||
		store.messages[0].SenderID != courier ||
		store.messages[0].RecipientID != requester {
		t.Fatalf("stored = %+v", store.messages)
	}
	if store.targets[[2]int64{requesterChat, 101}] != o.ID || store.selections[courier] != o.ID {
		t.Fatalf("routing state: targets=%v selections=%v", store.targets, store.selections)
	}
}

func TestReplyRoutesToTheRepliedOrder(t *testing.T) {
	a, b := order(Delivering), order(AwaitingPickup)
	r, sender, store, _ := newRelay(a, b)
	store.targets[[2]int64{requesterChat, 55}] = b.ID
	msg := text(requesterChat, "Thanks")
	msg.ReplyToMessage = &telegram.Message{MessageID: 55}
	if err := r.HandleMessage(context.Background(), msg); err != nil {
		t.Fatal(err)
	}
	if len(store.messages) != 1 || store.messages[0].OrderID != b.ID || sender.sent[0].chatID != courierChat {
		t.Fatalf("stored = %+v sent = %+v", store.messages, sender.sent)
	}
}

func TestSelectionRoutesPlainMessages(t *testing.T) {
	a, b := order(Delivering), order(AwaitingPickup)
	r, _, store, _ := newRelay(a, b)
	store.selections[requester] = a.ID
	if err := r.HandleMessage(context.Background(), text(requesterChat, "Where are you?")); err != nil {
		t.Fatal(err)
	}
	if len(store.messages) != 1 || store.messages[0].OrderID != a.ID {
		t.Fatalf("stored = %+v", store.messages)
	}
}

func TestSeveralOpenOrdersAskWhichOne(t *testing.T) {
	r, sender, store, _ := newRelay(order(Delivering), order(AwaitingPickup))
	if err := r.HandleMessage(context.Background(), text(requesterChat, "Hello")); err != nil {
		t.Fatal(err)
	}
	if len(store.messages) != 0 || len(sender.sent) != 1 || sender.sent[0].text != chooseReply ||
		len(sender.sent[0].keyboard.Rows) != 2 {
		t.Fatalf("sent = %+v", sender.sent)
	}
}

func TestRelayRefusals(t *testing.T) {
	unassigned := order("awaiting_courier")
	unassigned.CourierID = uuid.Nil
	ended := order(Completed)
	ended.EndedAt = now.Add(-GracePeriod - time.Minute)
	cases := []struct {
		name   string
		orders []Order
		chatID int64
		body   string
		reply  int64 // reply target message ID, or 0
		want   string
	}{
		{"unlinked sender", []Order{order(Delivering)}, 9, "hi", 0, notLinkedReply},
		{"no open chats", []Order{unassigned}, requesterChat, "hi", 0, noChatsReply},
		{"chat closed after grace", []Order{ended}, requesterChat, "hi", 0, noChatsReply},
		{"stranger", []Order{order(Delivering)}, strangerChat, "hi", 0, noChatsReply},
		{"non-text", []Order{order(Delivering)}, requesterChat, "", 0, textOnlyReply},
		{"too long", []Order{order(Delivering)}, requesterChat, strings.Repeat("a", maxMessageLength), 0, tooLongReply},
		{"reply to closed order", []Order{ended}, requesterChat, "hi", 55, closedReply},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, sender, store, _ := newRelay(tc.orders...)
			msg := text(tc.chatID, tc.body)
			if tc.reply != 0 {
				store.targets[[2]int64{tc.chatID, tc.reply}] = tc.orders[0].ID
				msg.ReplyToMessage = &telegram.Message{MessageID: tc.reply}
			}
			if err := r.HandleMessage(context.Background(), msg); err != nil {
				t.Fatal(err)
			}
			if len(store.messages) != 0 || len(sender.sent) != 1 || sender.sent[0].text != tc.want ||
				sender.sent[0].chatID != tc.chatID {
				t.Fatalf("sent = %+v", sender.sent)
			}
		})
	}
}

func TestRelayDeliveryFailures(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		unlink     bool
		wantReply  string
		unlinkPeer bool
	}{
		{"recipient not connected", nil, true, notConnectedReply, false},
		{
			"recipient blocked the bot",
			&telegram.APIError{Code: 403, Description: "Forbidden: bot was blocked by the user"},
			false,
			unreachableReply,
			true,
		},
		{"transient", errors.New("connection reset"), false, retryReply, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, sender, store, links := newRelay(order(Delivering))
			if tc.unlink {
				delete(links.chats, requester)
			}
			if tc.err != nil {
				sender.errs[requesterChat] = tc.err
			}
			if err := r.HandleMessage(context.Background(), text(courierChat, "hi")); err != nil {
				t.Fatal(err)
			}
			wantRecords := 1
			if tc.unlink {
				wantRecords = 0
			}
			if len(store.messages) != wantRecords || len(sender.sent) != 1 || sender.sent[0].text != tc.wantReply ||
				sender.sent[0].chatID != courierChat {
				t.Fatalf("sent = %+v stored = %+v", sender.sent, store.messages)
			}
			if (len(links.unlinked) == 1) != tc.unlinkPeer {
				t.Fatalf("unlinked = %v", links.unlinked)
			}
		})
	}
}

func TestShowChatsAndSelect(t *testing.T) {
	a, b := order(Delivering), order(AwaitingPickup)
	r, sender, store, _ := newRelay(a, b)
	if err := r.ShowChats(context.Background(), text(courierChat, "/chat")); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].keyboard == nil || len(sender.sent[0].keyboard.Rows) != 2 {
		t.Fatalf("sent = %+v", sender.sent)
	}

	cb := &telegram.CallbackQuery{
		ID:      "cb",
		From:    telegram.User{ID: courierChat},
		Data:    selectPrefix + b.ID.String(),
		Message: text(courierChat, ""),
	}
	if err := r.HandleCallback(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if store.selections[courier] != b.ID || len(sender.answered) != 1 || sender.answered[0] != "" {
		t.Fatalf("selections=%v answered=%v", store.selections, sender.answered)
	}
	if last := sender.sent[len(sender.sent)-1].text; last != "Your messages now go to your requester for the Fine Food order." {
		t.Fatalf("confirmation = %q", last)
	}

	// A stranger can't select someone else's order.
	cb.Message = text(strangerChat, "")
	cb.From.ID = strangerChat
	if err := r.HandleCallback(context.Background(), cb); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.selections[stranger]; ok || sender.answered[1] != closedReply {
		t.Fatalf("stranger selected: %v answered=%v", store.selections, sender.answered)
	}
}

func TestCallbackRejectsForwardedButtonsAndGroupChats(t *testing.T) {
	o := order(Delivering)
	for _, tc := range []struct {
		name string
		chat telegram.Chat
		from int64
	}{
		{"another user", telegram.Chat{ID: courierChat, Type: "private"}, strangerChat},
		{"group", telegram.Chat{ID: courierChat, Type: "group"}, courierChat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, sender, store, _ := newRelay(o)
			cb := &telegram.CallbackQuery{
				ID:      "cb",
				From:    telegram.User{ID: tc.from},
				Data:    selectPrefix + o.ID.String(),
				Message: &telegram.Message{Chat: tc.chat},
			}
			if err := r.HandleCallback(context.Background(), cb); err != nil {
				t.Fatal(err)
			}
			if len(store.selections) != 0 || len(sender.sent) != 0 {
				t.Fatalf("invalid callback changed state: selections=%v sent=%v", store.selections, sender.sent)
			}
		})
	}
}

func TestFeedbackRetryDoesNotRepeatRecipientDelivery(t *testing.T) {
	r, sender, _, _ := newRelay(order(Delivering))
	sender.errs[requesterChat] = errors.New("recipient connection reset")
	sender.errs[courierChat] = errors.New("sender connection reset")
	err := r.HandleMessage(context.Background(), text(courierChat, "hi"))
	var retry *telegram.RetryError
	if !errors.As(err, &retry) {
		t.Fatalf("HandleMessage = %v, want RetryError", err)
	}
	delete(sender.errs, requesterChat)
	delete(sender.errs, courierChat)
	if err := retry.Retry(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].chatID != courierChat || sender.sent[0].text != retryReply {
		t.Fatalf("retry sent = %+v; want only sender feedback", sender.sent)
	}
}

func TestPermanentFeedbackFailureIsConsumed(t *testing.T) {
	r, sender, _, _ := newRelay()
	sender.errs[courierChat] = &telegram.APIError{Code: 403, Description: "bot blocked"}
	if err := r.ShowChats(context.Background(), text(courierChat, "/chat")); err != nil {
		t.Fatalf("permanent failure should not retry: %v", err)
	}
}
