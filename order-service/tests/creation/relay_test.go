//go:build integration

package creation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	app "github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/order"
	"github.com/AY2627S1-CS3219-P1/FoC/order-service/internal/outbox"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

type crashAcknowledgement struct{ outbox.Repository }

func (crashAcknowledgement) Published(context.Context, outbox.Fact, time.Time) error {
	return errors.New("crash after broker acknowledgement")
}

type lostBrokerResponse struct{ publisher *kafka.Publisher }

func (p lostBrokerResponse) Publish(ctx context.Context, topic string, key []byte, message proto.Message) error {
	if err := p.publisher.Publish(ctx, topic, key, message); err != nil {
		return err
	}
	return errors.New("lost broker response")
}

func topicOffset(t *testing.T, address, topic string) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		connection, err := kafkago.DialLeader(ctx, "tcp", address, topic, 0)
		if err == nil {
			offset, readErr := connection.ReadLastOffset()
			_ = connection.Close()
			if readErr == nil {
				return offset
			}
			err = readErr
		}
		select {
		case <-ctx.Done():
			t.Fatalf("topic leader was not ready: %v", err)
			return 0
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func topicMessages(t *testing.T, address, topic string, offset int64, n int) []kafkago.Message {
	t.Helper()
	reader := kafkago.NewReader(kafkago.ReaderConfig{Brokers: []string{address}, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1000000, MaxWait: 100 * time.Millisecond})
	defer reader.Close()
	if err := reader.SetOffset(offset); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result := make([]kafkago.Message, 0, n)
	for range n {
		message, err := reader.ReadMessage(ctx)
		if err != nil {
			t.Fatal(err)
		}
		result = append(result, message)
	}
	return result
}

func expireLocally(t *testing.T, s *scenario, id string) {
	t.Helper()
	// Later mutation RPCs are outside this delivery. The existing aggregate builds
	// a genuine terminal transaction so the real relay can be exercised now.
	err := app.NewMutator(s.repo).Mutate(s.ctx, id, func(o app.Order, _ []app.Offer) (*app.Change, error) {
		after := o
		after.Status = app.Expired
		after.Revision++
		now := s.clock.Now()
		after.UpdatedAt = now
		after.ClosedAt = &now
		change, err := app.NewChange(&o, after, app.Actor{Kind: app.System}, app.Expire, nil, uuid.NewString)
		return &change, err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOrderOutboxWithKafka(t *testing.T) {
	f := databaseFixture(t)
	address := kafkaBroker(t, f.ctx)
	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: []string{address}})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	store := outbox.NewPostgres(f.pool)
	reopen := func() {
		if _, err := f.pool.Exec(f.ctx, "UPDATE order_outbox SET lease_until=now()-interval '1 second' WHERE published_at IS NULL"); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("terminal outcome publishes through Kafka without settlement RPC", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		order, err := create(c, f.ctx, input(s.clock.Now()))
		if err != nil {
			t.Fatal(err)
		}
		statuses := topicOffset(t, address, "order.status_changed")
		outcomes := topicOffset(t, address, "order.outcome")
		expireLocally(t, s, order.Id)
		if err := outbox.New(store, publisher).Drain(f.ctx, 50); err != nil {
			t.Fatal(err)
		}
		messages := topicMessages(t, address, "order.status_changed", statuses, 3)
		for i, message := range messages {
			var status events.OrderStatusChanged
			if err := proto.Unmarshal(message.Value, &status); err != nil {
				t.Fatal(err)
			}
			if status.OrderId != order.Id || string(message.Key) != order.Id || status.Revision != int64(i+1) || status.PickupLocationName != "Campus location" {
				t.Fatalf("incorrect status fact: %+v", &status)
			}
		}
		message := topicMessages(t, address, "order.outcome", outcomes, 1)[0]
		var outcome events.OrderOutcome
		if err := proto.Unmarshal(message.Value, &outcome); err != nil {
			t.Fatal(err)
		}
		if string(message.Key) != order.Id || outcome.OrderId != order.Id || outcome.Outcome != events.OrderOutcomeType_ORDER_OUTCOME_TYPE_EXPIRED || outcome.ReservedCredits != 10 {
			t.Fatalf("incorrect outcome: %+v", &outcome)
		}
		var pending int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE published_at IS NULL").Scan(&pending); err != nil || pending != 0 {
			t.Fatal("unpublished facts remain")
		}
		if s.remotes.releaseCalls != 0 {
			t.Fatal("opened Order used release RPC")
		}
	})
	for _, failure := range []string{"lost broker response", "local acknowledgement crash"} {
		t.Run(failure, func(t *testing.T) {
			s := newScenario(t, f)
			c := mount(t, f, s.creator)
			if _, err := create(c, f.ctx, input(s.clock.Now())); err != nil {
				t.Fatal(err)
			}
			offset := topicOffset(t, address, "order.status_changed")
			failing := outbox.New(store, lostBrokerResponse{publisher: publisher})
			if failure == "local acknowledgement crash" {
				failing = outbox.New(crashAcknowledgement{store}, publisher)
			}
			if err := failing.Drain(f.ctx, 1); err == nil {
				t.Fatal("crash not injected")
			}
			reopen()
			if err := outbox.New(outbox.NewPostgres(f.pool), publisher).Drain(f.ctx, 50); err != nil {
				t.Fatal(err)
			}
			messages := topicMessages(t, address, "order.status_changed", offset, 3)
			first, second := new(events.OrderStatusChanged), new(events.OrderStatusChanged)
			if err := proto.Unmarshal(messages[0].Value, first); err != nil {
				t.Fatal(err)
			}
			if err := proto.Unmarshal(messages[1].Value, second); err != nil {
				t.Fatal(err)
			}
			if first.EventId != second.EventId || first.Revision != second.Revision {
				t.Fatal("duplicate publication changed identity")
			}
		})
	}
	t.Run("unreachable broker leaves durable facts for recovery", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		if _, err := create(c, f.ctx, input(s.clock.Now())); err != nil {
			t.Fatal(err)
		}
		unavailable, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: []string{"127.0.0.1:1"}})
		if err != nil {
			t.Fatal(err)
		}
		defer unavailable.Close()
		deadline, cancel := context.WithTimeout(f.ctx, 100*time.Millisecond)
		defer cancel()
		if err := outbox.New(store, unavailable).Drain(deadline, 1); err == nil {
			t.Fatal("unavailable broker acknowledged")
		}
		var published int
		if err := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM order_outbox WHERE published_at IS NOT NULL").Scan(&published); err != nil || published != 0 {
			t.Fatal("lost pending facts")
		}
		reopen()
		if err := outbox.New(store, publisher).Drain(f.ctx, 50); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("competing workers claim one earliest fact per Order", func(t *testing.T) {
		s := newScenario(t, f)
		c := mount(t, f, s.creator)
		for range 2 {
			if _, err := create(c, f.ctx, input(s.clock.Now())); err != nil {
				t.Fatal(err)
			}
		}
		var workers sync.WaitGroup
		claims := make(chan *outbox.Fact, 50)
		errs := make(chan error, 50)
		for range 50 {
			workers.Go(func() {
				now := time.Now()
				fact, err := store.Claim(f.ctx, now, now.Add(time.Minute))
				if err != nil {
					errs <- err
				} else if fact != nil {
					claims <- fact
				}
			})
		}
		workers.Wait()
		close(errs)
		close(claims)
		for err := range errs {
			t.Fatal(err)
		}
		ids := map[string]bool{}
		for fact := range claims {
			if ids[fact.OrderID] || fact.Revision != 1 {
				t.Fatal("published past an outstanding earlier fact")
			}
			ids[fact.OrderID] = true
		}
		if len(ids) != 2 {
			t.Fatalf("claimed %d Orders", len(ids))
		}
		reopen()
		if err := outbox.New(store, publisher).Drain(f.ctx, 50); err != nil {
			t.Fatal(err)
		}
	})
}
