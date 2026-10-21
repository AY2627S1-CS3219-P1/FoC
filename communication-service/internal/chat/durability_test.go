package chat

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/google/uuid"
)

type failingRelayStore struct {
	*fakeStore
	stage    string
	failures int
}

type pendingMessageStore struct {
	*fakeStore
	saveFailures int
	getFailures  int
	attempts     []Message
}

func (s *pendingMessageStore) SaveMessage(ctx context.Context, m Message) error {
	s.attempts = append(s.attempts, m)
	if s.saveFailures > 0 {
		s.saveFailures--
		return errors.New("database unavailable")
	}
	return s.fakeStore.SaveMessage(ctx, m)
}

func (s *pendingMessageStore) GetOrder(ctx context.Context, id uuid.UUID) (Order, error) {
	if s.getFailures > 0 {
		s.getFailures--
		return Order{}, errors.New("database unavailable")
	}
	return s.fakeStore.GetOrder(ctx, id)
}

func TestRelayPendingMessageRevalidatesBeforePersistence(t *testing.T) {
	for _, change := range []string{"removed order", "closed order", "removed sender", "changed recipient"} {
		t.Run(change, func(t *testing.T) {
			o := order(Delivering)
			r, sender, store, _ := newRelay(o)
			pending := &pendingMessageStore{fakeStore: store, saveFailures: 1}
			r.Store = pending
			ctx := context.Background()
			var retry *telegram.RetryError
			if err := r.HandleMessage(ctx, text(courierChat, "private message")); !errors.As(err, &retry) {
				t.Fatalf("expected pending message retry, got %v", err)
			}

			switch change {
			case "removed order":
				delete(store.orders, o.ID)
			case "closed order":
				o.Status, o.EndedAt = Completed, now.Add(-2*GracePeriod)
				store.orders[o.ID] = o
			case "removed sender":
				o.CourierID = stranger
				store.orders[o.ID] = o
			case "changed recipient":
				o.RequesterID = stranger
				store.orders[o.ID] = o
			}
			if err := retry.Retry(ctx); err != nil {
				t.Fatal(err)
			}
			if len(store.messages) != 0 || len(pending.attempts) != 1 {
				t.Fatalf("persisted after eligibility changed: records = %d, attempts = %d", len(store.messages), len(pending.attempts))
			}
			if len(sender.sent) != 1 || sender.sent[0].chatID != courierChat || sender.sent[0].text != closedReply {
				t.Fatalf("expected closed feedback, sent = %+v", sender.sent)
			}
		})
	}
}

func TestRelayPendingMessageRetriesValidationFailure(t *testing.T) {
	r, sender, store, _ := newRelay(order(Delivering))
	pending := &pendingMessageStore{fakeStore: store, saveFailures: 1}
	r.Store = pending
	ctx := context.Background()
	var retry *telegram.RetryError
	if err := r.HandleMessage(ctx, text(courierChat, "private message")); !errors.As(err, &retry) {
		t.Fatalf("expected pending message retry, got %v", err)
	}

	pending.getFailures = 1
	err := retry.Retry(ctx)
	if !errors.As(err, &retry) {
		t.Fatalf("expected retryable validation error, got %v", err)
	}
	if len(pending.attempts) != 1 || len(store.messages) != 0 || len(sender.sent) != 0 {
		t.Fatalf("validation failure advanced delivery: attempts = %d, records = %d, sends = %d", len(pending.attempts), len(store.messages), len(sender.sent))
	}
	if err := retry.Retry(ctx); err != nil {
		t.Fatal(err)
	}
	if len(pending.attempts) != 2 || pending.attempts[0].ID != pending.attempts[1].ID || len(store.messages) != 1 || len(sender.sent) != 1 {
		t.Fatalf("retry did not resume original record: attempts = %+v, records = %d, sends = %d", pending.attempts, len(store.messages), len(sender.sent))
	}
}

func (s *failingRelayStore) fail(stage string) bool {
	if s.stage == stage && s.failures > 0 {
		s.failures--
		return true
	}
	return false
}

func (s *failingRelayStore) SaveMessage(ctx context.Context, m Message) error {
	// Simulate an insert that commits, but whose response is lost. Retrying
	// the same ID must leave exactly one dispute record.
	found := false
	for _, stored := range s.messages {
		found = found || stored.ID == m.ID
	}
	if !found {
		if err := s.fakeStore.SaveMessage(ctx, m); err != nil {
			return err
		}
	}
	if s.fail("message") {
		return errors.New("insert response lost")
	}
	return nil
}

func (s *failingRelayStore) Select(ctx context.Context, userID, orderID uuid.UUID, at time.Time) error {
	if s.fail("selection") {
		return errors.New("database unavailable")
	}
	return s.fakeStore.Select(ctx, userID, orderID, at)
}

func (s *failingRelayStore) SaveReplyTarget(ctx context.Context, chatID, messageID int64, orderID uuid.UUID, at time.Time) error {
	if s.fail("target") {
		return errors.New("database unavailable")
	}
	return s.fakeStore.SaveReplyTarget(ctx, chatID, messageID, orderID, at)
}

func TestRelayStorageRetriesDoNotResend(t *testing.T) {
	for _, stage := range []string{"message", "selection", "target"} {
		t.Run(stage, func(t *testing.T) {
			r, sender, store, _ := newRelay(order(Delivering))
			r.Store = &failingRelayStore{fakeStore: store, stage: stage, failures: 2}
			ctx := context.Background()
			err := r.HandleMessage(ctx, text(courierChat, "I'm at the lobby"))
			for attempt := 0; attempt < 2; attempt++ {
				var retry *telegram.RetryError
				if !errors.As(err, &retry) {
					t.Fatalf("attempt %d: expected retryable storage error, got %v", attempt, err)
				}
				wantSends := 0
				if stage == "target" {
					wantSends = 1
				}
				if len(sender.sent) != wantSends {
					t.Fatalf("attempt %d: sends = %d, want %d", attempt, len(sender.sent), wantSends)
				}
				if len(store.messages) != 1 {
					t.Fatalf("dispute records = %d, want 1 before delivery", len(store.messages))
				}
				err = retry.Retry(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(sender.sent) != 1 || len(store.messages) != 1 || len(store.targets) != 1 {
				t.Fatalf("sends = %d, records = %d, targets = %d", len(sender.sent), len(store.messages), len(store.targets))
			}
		})
	}
}

func TestRelayRetryRevalidatesOrderAndLinks(t *testing.T) {
	for _, stage := range []string{"message", "selection"} {
		for _, change := range []string{"closed order", "removed sender", "changed recipient", "sender unlinked", "sender relinked", "recipient unlinked", "recipient relinked"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				o := order(Delivering)
				r, sender, store, links := newRelay(o)
				r.Store = &failingRelayStore{fakeStore: store, stage: stage, failures: 1}
				ctx := context.Background()
				var retry *telegram.RetryError
				if err := r.HandleMessage(ctx, text(courierChat, "private message")); !errors.As(err, &retry) {
					t.Fatalf("expected pending storage retry, got %v", err)
				}
				if len(sender.sent) != 0 {
					t.Fatalf("sent before storage recovered: %+v", sender.sent)
				}

				wantReply := closedReply
				switch change {
				case "closed order":
					o.Status, o.EndedAt = Completed, now.Add(-2*GracePeriod)
				case "removed sender":
					o.CourierID = stranger
				case "changed recipient":
					o.RequesterID = stranger
				case "sender unlinked":
					delete(links.chats, courier)
					wantReply = notLinkedReply
				case "sender relinked":
					links.chats[courier] = 999
					wantReply = notLinkedReply
				case "recipient unlinked":
					delete(links.chats, requester)
					wantReply = notConnectedReply
				case "recipient relinked":
					links.chats[requester] = 999
				}
				store.orders[o.ID] = o
				if err := retry.Retry(ctx); err != nil {
					t.Fatal(err)
				}
				if len(sender.sent) != 1 {
					t.Fatalf("sends = %+v", sender.sent)
				}
				if change == "recipient relinked" {
					if sender.sent[0].chatID != 999 || sender.sent[0].text != relayedText(o, courier, "private message") {
						t.Fatalf("message did not use refreshed recipient: %+v", sender.sent)
					}
				} else if sender.sent[0].chatID != courierChat || sender.sent[0].text != wantReply {
					t.Fatalf("expected refusal %q, sent = %+v", wantReply, sender.sent)
				}
			})
		}
	}
}
