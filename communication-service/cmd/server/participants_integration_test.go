//go:build integration

package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/chat"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/notify"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A Telegram outage pauses notifications on the first send. The participants
// keep consuming meanwhile, so when Telegram returns the paused "head to
// pickup" is checked against the pickup made since and skipped.
func TestStillCurrentNotificationSkippedAfterOutageBacklog(t *testing.T) {
	dsn, brokerList := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_KAFKA_BROKERS")
	if dsn == "" || brokerList == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_KAFKA_BROKERS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool := notificationTestPool(t, ctx, dsn)
	if _, err := (&notify.PostgresDeliveries{Queries: db.New(pool)}).Start(
		ctx,
		time.Now().Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}
	requester, courier := uuid.New(), uuid.New()
	const courierChat = 4242
	if _, err := pool.Exec(ctx,
		`INSERT INTO telegram_links (user_id, chat_id, linked_at) VALUES ($1, $2, now())`,
		courier, courierChat); err != nil {
		t.Fatal(err)
	}

	var telegramUp atomic.Bool
	var telegramFailed atomic.Bool
	var mu sync.Mutex
	var delivered []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sendMessage") {
			_, _ = io.WriteString(w, `{"ok":true,"result":true}`)
			return
		}
		if !telegramUp.Load() {
			telegramFailed.Store(true)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = io.WriteString(w, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
			return
		}
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		delivered = append(delivered, string(body))
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":4242,"type":"private"}}}`)
	}))
	defer api.Close()

	brokers := strings.Split(brokerList, ",")
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	runCtx, stop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stop(); workers.Wait() }()
	chats := &chat.PostgresStore{Pool: pool}
	startNotifications(runCtx, config{kafkaBrokers: brokers, telegramToken: "1:test", telegramAPI: api.URL}, log,
		&linking.Service{Store: &linking.PostgresStore{Pool: pool}, Now: time.Now},
		chats, pool, &notify.PostgresEmailQueue{Pool: pool}, true, &workers)

	var published kafkago.Message
	writer := &kafkago.Writer{
		Addr: kafkago.TCP(brokers...), Balancer: &kafkago.Hash{}, RequiredAcks: kafkago.RequireAll,
		Completion: func(messages []kafkago.Message, _ error) { published = messages[0] },
	}
	defer func() { _ = writer.Close() }()
	publish := func(e *eventsv1.OrderStatusChanged) kafkago.Message {
		t.Helper()
		payload, err := proto.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteMessages(ctx,
			kafkago.Message{Topic: notify.Topic, Key: []byte(e.OrderId), Value: payload}); err != nil {
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

	orderID := uuid.New()
	event := func(revision int64, previous, status eventsv1.OrderStatus, actor uuid.UUID) *eventsv1.OrderStatusChanged {
		return &eventsv1.OrderStatusChanged{
			EventId:        uuid.NewString(),
			OrderId:        orderID.String(),
			Revision:       revision,
			PreviousStatus: previous,
			Status:         status,
			Actor: &eventsv1.Actor{
				Type:   eventsv1.ActorType_ACTOR_TYPE_USER,
				UserId: proto.String(actor.String()),
			},
			RequesterId:        requester.String(),
			CourierId:          proto.String(courier.String()),
			PickupLocationName: "COM2",
			OccurredAt:         timestamppb.Now(),
		}
	}
	// The requester accepts the courier's offer: "head to pickup" for the courier.
	publish(event(3, eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
		eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP, requester))
	await("a failed Telegram send", telegramFailed.Load)
	// The courier picks up while that message waits for Telegram.
	pickup := publish(event(4, eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		eventsv1.OrderStatus_ORDER_STATUS_DELIVERING, courier))
	await("the participants to reach the pickup", func() bool {
		revision, found, err := chats.Revision(ctx, orderID)
		return err == nil && found && revision == 4
	})

	telegramUp.Store(true)
	await("notifications to pass the pickup", func() bool { return committed(pickup) })
	mu.Lock()
	defer mu.Unlock()
	for _, body := range delivered {
		if strings.Contains(body, "Head to the pickup location") {
			t.Fatalf("superseded notification was sent after the outage: %s", body)
		}
	}
}

// Hold the participant row so the independent consumer cannot apply the
// unassignment. Notifications must apply it too, rather than interpreting the
// previous assignment as a later reassignment and acknowledging without sending.
func TestUnassignmentWaitsForParticipantsBeforeNotifying(t *testing.T) {
	dsn, brokers := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_KAFKA_BROKERS")
	if dsn == "" || brokers == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_KAFKA_BROKERS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool := notificationTestPool(t, ctx, dsn)
	orderID, requester, courier := uuid.New(), uuid.New(), uuid.New()
	chats := &chat.PostgresStore{Pool: pool}
	if err := chats.SaveOrder(ctx, chat.Order{ID: orderID, RequesterID: requester, CourierID: courier,
		Status: "awaiting_pickup", Revision: 3}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(
		ctx,
		`INSERT INTO telegram_links (user_id, chat_id, linked_at) VALUES ($1, 4242, now())`,
		courier,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := (&notify.PostgresDeliveries{Queries: db.New(pool)}).Start(
		ctx,
		time.Now().Add(-time.Second),
	); err != nil {
		t.Fatal(err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM order_participants WHERE order_id = $1 FOR UPDATE`, orderID); err != nil {
		t.Fatal(err)
	}

	var sent atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent.Add(1)
		_, _ = io.WriteString(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":4242,"type":"private"}}}`)
	}))
	defer api.Close()

	runCtx, stop := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { stop(); workers.Wait() }()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	startNotifications(
		runCtx,
		config{kafkaBrokers: strings.Split(brokers, ","), telegramToken: "1:test", telegramAPI: api.URL},
		log,
		&linking.Service{Store: &linking.PostgresStore{Pool: pool}, Now: time.Now},
		chats,
		pool,
		&notify.PostgresEmailQueue{Pool: pool},
		true,
		&workers,
	)

	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: strings.Split(brokers, ",")})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	e := &eventsv1.OrderStatusChanged{
		EventId:        uuid.NewString(),
		OrderId:        orderID.String(),
		Revision:       4,
		PreviousStatus: eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		Status:         eventsv1.OrderStatus_ORDER_STATUS_AWAITING_COURIER,
		Actor: &eventsv1.Actor{
			Type:   eventsv1.ActorType_ACTOR_TYPE_USER,
			UserId: proto.String(requester.String()),
		},
		RequesterId:       requester.String(),
		PreviousCourierId: proto.String(courier.String()),
		OccurredAt:        timestamppb.Now(),
	}
	if err := publisher.Publish(ctx, notify.Topic, []byte(e.OrderId), e); err != nil {
		t.Fatal(err)
	}

	// Both consumers must wait to apply the event before notifications can
	// decide whether this courier has returned. The old wiring blocks only one.
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	blocked := 0
waitForConsumers:
	for {
		if err := pool.QueryRow(ctx, `SELECT count(DISTINCT a.pid)
            FROM pg_stat_activity a
            JOIN pg_locks l ON l.pid = a.pid
            JOIN pg_class c ON c.oid = l.relation
            WHERE a.datname = current_database() AND a.wait_event_type = 'Lock'
              AND a.query LIKE '%INSERT INTO order_participants%'
              AND c.relname = 'order_participants' AND c.relnamespace = current_schema()::regnamespace`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked >= 2 {
			break
		}
		select {
		case <-deadline.C:
			break waitForConsumers
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-ticker.C:
		}
	}

	// Release before assertions so consumer shutdown cannot wait on our lock.
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if blocked < 2 {
		t.Fatalf(
			"notification consumer did not wait for participants: blocked consumers=%d, sent=%d",
			blocked,
			sent.Load(),
		)
	}
	for sent.Load() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("removed courier was never notified")
		case <-ticker.C:
		}
	}
}
