package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Use an isolated broker and pre-created empty topic with three partitions.
// This verifies that untouched empty partitions with no group offset cannot
// incorrectly block the replay completion barrier.
func TestReplayOffsetBarrierIntegration(t *testing.T) {
	brokers, topic := os.Getenv("TEST_KAFKA_BROKERS"), os.Getenv("TEST_KAFKA_TOPIC")
	if brokers == "" || topic == "" {
		t.Skip("isolated TEST_KAFKA_BROKERS and TEST_KAFKA_TOPIC are not set")
	}
	t.Setenv("PERF_KAFKA_BROKERS", brokers)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	publisher, err := kafka.NewPublisher(kafka.PublisherConfig{Brokers: strings.Split(brokers, ",")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = publisher.Close() }()
	group := fmt.Sprint("perf-barrier-", time.Now().UnixNano())
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{Brokers: strings.Split(brokers, ","), GroupID: group})
	if err != nil {
		t.Fatal(err)
	}
	observed := make(chan struct{}, 1)
	err = kafka.Register(
		subscriber,
		topic,
		func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) },
		func(_ context.Context, _ kafka.Metadata, _ *wrapperspb.StringValue) error {
			select {
			case observed <- struct{}{}:
			default:
			}
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- subscriber.Run(ctx) }()
	defer func() { cancel(); <-stopped }()
	var message proto.Message = wrapperspb.String("committed effect observed separately")
	if err := publisher.Publish(ctx, topic, []byte("one-key"), message); err != nil {
		t.Fatal(err)
	}
	select {
	case <-observed:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	out := t.TempDir()
	p := eventProbe{eventInput: eventInput{Topic: topic}, id: fixtureID(0, "offset-test", 0), consumer: group}
	if err := waitForConsumer(ctx, p, out); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(out, "replay-offsets-"+p.id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var proof struct {
		Target map[string]int64 `json:"postPublicationHighWater"`
	}
	if err := json.Unmarshal(data, &proof); err != nil {
		t.Fatal(err)
	}
	if len(proof.Target) != 3 {
		t.Fatal("test requires a three-partition topic")
	}
	untouched := 0
	for _, offset := range proof.Target {
		if offset == 0 {
			untouched++
		}
	}
	if untouched != 2 {
		t.Fatal("topic was not empty before the single-key barrier test")
	}
}
