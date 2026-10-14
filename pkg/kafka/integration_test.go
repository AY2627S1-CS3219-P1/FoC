package kafka

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type cancelAfterCommit struct {
	reader
	cancel context.CancelFunc
}

func (r cancelAfterCommit) CommitMessages(ctx context.Context, messages ...kafkago.Message) error {
	err := r.reader.CommitMessages(ctx, messages...)
	if err == nil {
		r.cancel()
	}
	return err
}

// Requires an isolated broker and an empty, pre-created topic. It never creates
// or deletes topics, and uses a unique consumer group for each test run.
func TestKafkaIntegration(t *testing.T) {
	brokers, topic := os.Getenv("TEST_KAFKA_BROKERS"), os.Getenv("TEST_KAFKA_TOPIC")
	if brokers == "" || topic == "" {
		t.Skip("set TEST_KAFKA_BROKERS and TEST_KAFKA_TOPIC for an isolated broker and empty topic")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	p, err := NewPublisher(PublisherConfig{Brokers: strings.Split(brokers, ",")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			t.Errorf("close publisher: %v", err)
		}
	}()
	value := fmt.Sprintf("integration-%d", time.Now().UnixNano())
	if err := p.Publish(ctx, topic, []byte(value), wrapperspb.String(value)); err != nil {
		t.Fatal(err)
	}
	cfg := SubscriberConfig{Brokers: strings.Split(brokers, ","), GroupID: value}
	first, err := NewSubscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var failedOffset int64
	if err := Register(first, topic, func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) }, func(_ context.Context, meta Metadata, message *wrapperspb.StringValue) error {
		if message.Value != value || string(meta.Key) != value {
			t.Fatal("expected empty test topic and matching payload/key")
		}
		failedOffset = meta.Offset
		return errTest
	}); err != nil {
		t.Fatal(err)
	}
	if err := first.Run(ctx); !errors.Is(err, errTest) {
		t.Fatalf("handler failure: %v", err)
	}
	second, err := NewSubscriber(cfg)
	if err != nil {
		t.Fatal(err)
	}
	replayCtx, stop := context.WithCancel(ctx)
	defer stop()
	second.newReader = func(cfg kafkago.ReaderConfig) reader {
		return cancelAfterCommit{reader: kafkago.NewReader(cfg), cancel: stop}
	}
	replayed := false
	if err := Register(second, topic, func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) }, func(_ context.Context, meta Metadata, message *wrapperspb.StringValue) error {
		if meta.Offset != failedOffset || message.Value != value {
			t.Fatal("uncommitted message was not replayed")
		}
		replayed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := second.Run(replayCtx); err != nil {
		t.Fatal(err)
	}
	if !replayed || ctx.Err() != nil {
		t.Fatal("replay did not complete")
	}
}
