package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/chat"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/google/uuid"
)

type openChatLinks struct {
	chat.Links
	link linking.Link
	err  error
}

func (l *openChatLinks) GetByChat(context.Context, int64) (linking.Link, error) { return l.link, l.err }

type openChatStore struct {
	chat.Store
	order             chat.Order
	getErr, selectErr error
	selectCalls       int
}

func (s *openChatStore) GetOrder(context.Context, uuid.UUID) (chat.Order, error) {
	return s.order, s.getErr
}

func (s *openChatStore) Select(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	s.selectCalls++
	return s.selectErr
}

type openChatSender struct {
	chat.Sender
	fakeSender
	err error
}

func (s *openChatSender) Send(
	_ context.Context,
	_ int64,
	text string,
	_ *telegram.InlineKeyboard,
) (telegram.Message, error) {
	if s.err != nil {
		return telegram.Message{}, s.err
	}
	s.sent = append(s.sent, text)
	return telegram.Message{}, nil
}

func retryOpenChat(t *testing.T, err error) func(context.Context) error {
	t.Helper()
	var retry *telegram.RetryError
	if !errors.As(err, &retry) {
		t.Fatalf("expected retained update, got %v", err)
	}
	return retry.Retry
}

func chatRetryFixture() (*Handler, *openChatLinks, *openChatStore, *openChatSender, telegram.Update) {
	h, _ := newHandler(&fakeLinker{})
	userID := uuid.New()
	links := &openChatLinks{link: linking.Link{UserID: userID, ChatID: 42}}
	store := &openChatStore{order: chat.Order{
		ID: uuid.New(), RequesterID: userID, CourierID: uuid.New(), Status: chat.Delivering,
	}}
	sender := &openChatSender{}
	h.Sender = sender
	h.Chat = &chat.Relay{Links: links, Store: store, Sender: sender, Log: h.Log, Now: time.Now}
	return h, links, store, sender, message("private", "/start chat_"+store.order.ID.String())
}

func TestChatStartRetriesStorageAndRevalidates(t *testing.T) {
	for _, stage := range []string{"link", "order", "selection"} {
		for _, change := range []string{"none", "unlinked", "nonparticipant", "closed", "missing"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				ctx := context.Background()
				h, links, store, sender, update := chatRetryFixture()
				outage := errors.New("temporary storage failure")
				switch stage {
				case "link":
					links.err = outage
				case "order":
					store.getErr = outage
				case "selection":
					store.selectErr = outage
				}
				first := retryOpenChat(t, h.Handle(ctx, update))
				second := retryOpenChat(t, first(ctx))
				if len(sender.sent) != 0 {
					t.Fatalf("confirmation before selection: %v", sender.sent)
				}
				links.err, store.getErr, store.selectErr = nil, nil, nil
				switch change {
				case "unlinked":
					links.err = linking.ErrNotLinked
				case "nonparticipant":
					links.link.UserID = uuid.New()
				case "closed":
					store.order.Status = "expired"
				case "missing":
					store.getErr = chat.ErrNotFound
				}
				before := store.selectCalls
				if err := second(ctx); err != nil {
					t.Fatal(err)
				}
				if len(sender.sent) != 1 {
					t.Fatalf("want one final reply, got %v", sender.sent)
				}
				if change == "none" {
					if store.selectCalls != before+1 || sender.sent[0] == startReply {
						t.Fatalf("chat was not opened: calls=%d replies=%v", store.selectCalls, sender.sent)
					}
				} else {
					if store.selectCalls != before {
						t.Fatal("selected chat after eligibility changed")
					}
					if change != "closed" && sender.sent[0] != startReply {
						t.Fatalf("revealed inaccessible order: %v", sender.sent)
					}
				}
			})
		}
	}
}

func TestChatStartRetriesOnlyReplyAfterSelection(t *testing.T) {
	ctx := context.Background()
	h, links, store, sender, update := chatRetryFixture()
	sender.err = errors.New("Telegram temporarily unavailable")
	retry := retryOpenChat(t, h.Handle(ctx, update))
	if store.selectCalls != 1 {
		t.Fatalf("selection calls=%d", store.selectCalls)
	}
	links.err = errors.New("storage must not be consulted for feedback retry")
	sender.err = nil
	if err := retry(ctx); err != nil {
		t.Fatal(err)
	}
	if store.selectCalls != 1 || len(sender.sent) != 1 {
		t.Fatalf("repeated selection or lost feedback: calls=%d sent=%v", store.selectCalls, sender.sent)
	}
}

func TestChatStartStorageRetryTransitionsToReplyOnlyRetry(t *testing.T) {
	ctx := context.Background()
	h, links, store, sender, update := chatRetryFixture()
	store.selectErr = errors.New("database unavailable")
	retrySelection := retryOpenChat(t, h.Handle(ctx, update))

	store.selectErr = nil
	sender.err = errors.New("Telegram unavailable")
	retryReply := retryOpenChat(t, retrySelection(ctx))
	before := store.selectCalls

	links.err = errors.New("feedback must not recheck storage")
	sender.err = nil
	if err := retryReply(ctx); err != nil {
		t.Fatal(err)
	}
	if store.selectCalls != before || len(sender.sent) != 1 {
		t.Fatalf("repeated selection or lost feedback: calls=%d sent=%v", store.selectCalls, sender.sent)
	}
}
