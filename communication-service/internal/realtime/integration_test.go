package realtime

import (
	"context"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	pb "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	credits "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/credit/events/v1"
	orders "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Two independent hubs/HTTP replicas share only PostgreSQL and Kafka.
func TestKafkaChangesReachBothReplicasAndRecover(t *testing.T) {
	url, broker := os.Getenv("TEST_DATABASE_URL"), os.Getenv("TEST_KAFKA_BROKERS")
	if url == "" || broker == "" {
		t.Skip("TEST_DATABASE_URL and TEST_KAFKA_BROKERS required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: []string{broker}})
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close() //nolint:errcheck
	hubs := []*Hub{NewHub(), NewHub()}
	fanouts := []*Fanout{{Pool: pool, Hub: hubs[0]}, {Pool: pool, Hub: hubs[1]}}
	listenerCtx, stopListeners := context.WithCancel(ctx)
	defer stopListeners()
	stopped := make(chan struct{}, 2)
	for _, f := range fanouts {
		go func() { f.Run(listenerCtx); stopped <- struct{}{} }()
	}
	defer func() { stopListeners(); <-stopped; <-stopped }()
	for _, h := range hubs {
		for {
			h.mu.Lock()
			ready := h.ready
			h.mu.Unlock()
			if ready {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	consumerCtx, stopConsumer := context.WithCancel(ctx)
	defer stopConsumer()
	consumer := &Consumer{Fanout: fanouts[0]}
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{
		Brokers: []string{broker}, GroupID: "realtime-test-" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := kafka.Register(subscriber, "credit.account_changed",
		func() *credits.AccountChanged { return new(credits.AccountChanged) }, consumer.Account); err != nil {
		t.Fatal(err)
	}
	if err := kafka.Register(subscriber, "order.changed",
		func() *orders.OrderChanged { return new(orders.OrderChanged) }, consumer.Order); err != nil {
		t.Fatal(err)
	}
	consumed := make(chan struct{})
	go func() {
		if err := subscriber.Run(consumerCtx); err != nil && consumerCtx.Err() == nil {
			t.Error(err)
		}
		close(consumed)
	}()
	defer func() { stopConsumer(); <-consumed }()
	user := uuid.NewString()
	streams := make([]*connect.ServerStreamForClient[pb.WatchChangesResponse], 0, 2)
	for _, h := range hubs {
		client := streamClient(t, h, auth.Caller{ID: user})
		stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close() //nolint:errcheck
		if !stream.Receive() || !stream.Msg().Ready {
			t.Fatalf("no ready: %v", stream.Err())
		}
		streams = append(streams, stream)
	}
	event := &credits.AccountChanged{
		EventId:    uuid.NewString(),
		UserId:     user,
		Revision:   2,
		OccurredAt: timestamppb.Now(),
	}
	if err := publisher.Publish(ctx, "credit.account_changed", []byte(user), event); err != nil {
		t.Fatal(err)
	}
	for _, stream := range streams {
		if !stream.Receive() || stream.Msg().Scopes[0] != pb.ChangeScope_CHANGE_SCOPE_CREDITS {
			t.Fatalf("credit fanout: %v", stream.Err())
		}
	}
	order := &orders.OrderChanged{
		EventId:          uuid.NewString(),
		OrderId:          uuid.NewString(),
		Revision:         1,
		RecipientUserIds: []string{user},
		OccurredAt:       timestamppb.Now(),
	}
	if err := publisher.Publish(ctx, "order.changed", []byte(order.OrderId), order); err != nil {
		t.Fatal(err)
	}
	for _, stream := range streams {
		if !stream.Receive() || stream.Msg().Scopes[0] != pb.ChangeScope_CHANGE_SCOPE_ORDERS {
			t.Fatalf("order fanout: %v", stream.Err())
		}
	}
	// Losing one listener closes its streams; a fresh subscription begins with
	// ready and the browser's corresponding current-state refetch.
	if _, err := pool.Exec(
		ctx,
		"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE application_name='foc-realtime-listener'",
	); err != nil {
		t.Fatal(err)
	}
	if streams[0].Receive() || connect.CodeOf(streams[0].Err()) != connect.CodeUnavailable {
		t.Fatalf("lost listener: %v", streams[0].Err())
	}
	for {
		hubs[0].mu.Lock()
		ready := hubs[0].ready
		hubs[0].mu.Unlock()
		if ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	client := streamClient(t, hubs[0], auth.Caller{ID: user})
	stream, err := client.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close() //nolint:errcheck
	if !stream.Receive() || !stream.Msg().Ready {
		t.Fatal("reconnect did not request current-state resync")
	}
}
