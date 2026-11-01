//go:build integration

package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database"
	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/notify"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// fakeTelegram fails the first sendMessage with a 502, then accepts the rest.
type fakeTelegram struct {
	mu    sync.Mutex
	calls int
	sent  []string
}

func (f *fakeTelegram) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls == 1 {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"ok":false,"error_code":502,"description":"Bad Gateway"}`)
		return
	}
	f.sent = append(f.sent, body.Text)
	_, _ = io.WriteString(w, `{"ok":true,"result":{}}`)
}

func (f *fakeTelegram) snapshot() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, append([]string(nil), f.sent...)
}

// TestNotificationsReplayAfterTelegramFailure needs a throwaway database in
// TEST_DATABASE_URL and a broker in TEST_KAFKA_BROKERS with the
// order.status_changed topic created.
func TestNotificationsReplayAfterTelegramFailure(t *testing.T) {
	dbURL, brokers := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_KAFKA_BROKERS")
	if dbURL == "" || brokers == "" {
		t.Skip("set TEST_DATABASE_URL and TEST_KAFKA_BROKERS")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	requester := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO telegram_links (user_id, chat_id, linked_at) VALUES ($1, $2, now())`,
		requester, time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	tg := &fakeTelegram{}
	srv := httptest.NewServer(tg)
	defer srv.Close()
	links := &linking.Service{Store: &linking.PostgresStore{Pool: pool}, Now: time.Now}
	handler := &notify.Handler{
		Sender:     telegram.NewClient("123:token", srv.URL, srv.Client()),
		Links:      links,
		Deliveries: &notify.PostgresDeliveries{Queries: db.New(pool)},
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        time.Now,
	}

	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: strings.Split(brokers, ",")})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	orderID := uuid.NewString()
	courier := uuid.NewString()
	event := &eventsv1.OrderStatusChanged{
		EventId:            uuid.NewString(),
		OrderId:            orderID,
		Revision:           5,
		PreviousStatus:     eventsv1.OrderStatus_ORDER_STATUS_AWAITING_PICKUP,
		Status:             eventsv1.OrderStatus_ORDER_STATUS_DELIVERING,
		Actor:              &eventsv1.Actor{Type: eventsv1.ActorType_ACTOR_TYPE_USER, UserId: proto.String(courier)},
		RequesterId:        requester.String(),
		CourierId:          proto.String(courier),
		PickupLocationName: "Fine Food",
		OccurredAt:         timestamppb.Now(),
	}
	// The same event twice: the second delivery must not send again.
	for range 2 {
		if err := publisher.Publish(ctx, notify.Topic, []byte(orderID), event); err != nil {
			t.Fatal(err)
		}
	}

	// A fresh group reads from the earliest offset. Events from earlier runs
	// are skipped because their recipients were already recorded as sent.
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runNotifications(runCtx, strings.Split(brokers, ","), "notify-test-"+uuid.NewString(),
			slog.New(slog.NewTextHandler(io.Discard, nil)), handler.Handle)
	}()

	deadline := time.Now().Add(time.Minute)
	for {
		calls, sent := tg.snapshot()
		if len(sent) >= 1 && calls >= 2 {
			// Give the duplicate delivery time to be processed.
			time.Sleep(2 * time.Second)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("calls=%d sent=%v", calls, sent)
		}
		time.Sleep(200 * time.Millisecond)
	}
	stop()
	<-done

	calls, sent := tg.snapshot()
	want := "Your order from Fine Food has been picked up and is on its way."
	if calls != 2 || len(sent) != 1 || sent[0] != want {
		t.Fatalf("calls=%d sent=%q", calls, sent)
	}
	var recorded int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM order_notifications_sent WHERE event_id = $1`, event.EventId).
		Scan(&recorded); err != nil ||
		recorded != 1 {
		t.Fatalf("recorded = %d, %v", recorded, err)
	}
}
