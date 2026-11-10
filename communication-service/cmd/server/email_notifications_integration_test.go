//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/chat"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/email"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/notify"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/users"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	usereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type recordingEmailSender struct {
	mu   sync.Mutex
	sent []email.Email
}

func (s *recordingEmailSender) Send(_ context.Context, message email.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, message)
	return nil
}

func (s *recordingEmailSender) snapshot() []email.Email {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]email.Email(nil), s.sent...)
}

// Uses the real startup path and a dedicated Kafka broker with both
// order.status_changed and users.create provisioned. The order's committed
// offset is observed before shutdown; its user event is published only after
// restarting, so delivery cannot depend on topic ordering or in-memory work.
func TestEmailNotificationAfterCommittedOutcomeAndConsumerRestart(t *testing.T) {
	dsn, brokerList := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_KAFKA_BROKERS")
	if dsn == "" || brokerList == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_KAFKA_BROKERS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "email_startup_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	// This test owns its schema and considers only events from this run.
	if _, err := (&notify.PostgresDeliveries{Queries: db.New(pool)}).Start(
		ctx,
		time.Now().Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}
	brokers := strings.Split(brokerList, ",")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	mailer := &recordingEmailSender{}
	start := func() func() {
		runCtx, stop := context.WithCancel(ctx)
		var workers sync.WaitGroup
		startNotifications(runCtx, config{kafkaBrokers: brokers}, log,
			&linking.Service{Store: &linking.PostgresStore{Pool: pool}, Now: time.Now},
			&chat.PostgresStore{Pool: pool}, pool, mailer, false, &workers)
		return sync.OnceFunc(func() { stop(); workers.Wait() })
	}
	stop := start()
	defer stop()

	// Completion gives the exact partition and offset whose commit must be
	// observed; an unrelated earlier commit cannot satisfy the assertion.
	var published kafkago.Message
	writer := &kafkago.Writer{
		Addr: kafkago.TCP(brokers...), Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll,
		Completion: func(messages []kafkago.Message, _ error) { published = messages[0] },
	}
	defer func() { _ = writer.Close() }()
	publish := func(topic, key string, message proto.Message) kafkago.Message {
		t.Helper()
		payload, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteMessages(
			ctx,
			kafkago.Message{Topic: topic, Key: []byte(key), Value: payload},
		); err != nil {
			t.Fatal(err)
		}
		return published
	}
	transport := &kafkago.Transport{}
	defer transport.CloseIdleConnections()
	client := &kafkago.Client{Addr: kafkago.TCP(brokers...), Transport: transport}
	committed := func(message kafkago.Message) bool {
		response, err := client.OffsetFetch(ctx, &kafkago.OffsetFetchRequest{
			GroupID: consumerGroup, Topics: map[string][]int{message.Topic: {message.Partition}},
		})
		if err != nil || response.Error != nil {
			return false
		}
		for _, partition := range response.Topics[message.Topic] {
			if partition.Error == nil && partition.CommittedOffset > message.Offset {
				return true
			}
		}
		return false
	}
	await := func(description string, ready func() bool) {
		t.Helper()
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for !ready() {
			select {
			case <-ctx.Done():
				t.Fatalf("timed out waiting for %s", description)
			case <-ticker.C:
			}
		}
	}
	requester, courier := uuid.NewString(), uuid.NewString()
	outcome := &eventsv1.OrderStatusChanged{
		EventId: uuid.NewString(), OrderId: uuid.NewString(), Revision: 5,
		PreviousStatus: eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
		Status:         eventsv1.OrderStatus_ORDER_STATUS_DELIVERED,
		Actor:          &eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_USER, UserId: proto.String(courier)},
		RequesterId:    requester, CourierId: proto.String(courier),
		PickupLocationName: "Fine Food", OccurredAt: timestamppb.Now(),
	}
	orderMessage := publish(notify.Topic, outcome.OrderId, outcome)
	await("the outcome's committed offset", func() bool { return committed(orderMessage) })
	var pending int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM notification_emails WHERE event_id = $1`, outcome.EventId).
		Scan(&pending); err != nil || pending != 1 || len(mailer.snapshot()) != 0 {
		t.Fatalf("committed outcome was not retained: pending=%d emails=%+v err=%v", pending, mailer.snapshot(), err)
	}
	stop()
	stopAfterRestart := start()
	defer stopAfterRestart()
	publish(users.Topic, requester, &usereventsv1.UserCreated{
		EventId: uuid.NewString(), UserId: requester, Email: "alice@foc.local", CreatedAt: timestamppb.Now(),
	})
	await("email delivery after the user event", func() bool {
		var sent int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM order_notifications_sent WHERE event_id = $1`, outcome.EventId).
			Scan(&sent)
		return err == nil && sent == 1
	})
	replay := publish(notify.Topic, outcome.OrderId, outcome)
	await("the replay's committed offset", func() bool { return committed(replay) })
	stopAfterRestart()
	sent := mailer.snapshot()
	if len(sent) != 1 || sent[0].To[0] != "alice@foc.local" ||
		!strings.HasPrefix(sent[0].TextBody, "Your order from Fine Food has been delivered.") {
		t.Fatalf("emails = %+v", sent)
	}
}
