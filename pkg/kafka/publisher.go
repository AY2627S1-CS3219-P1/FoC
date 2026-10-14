// Package kafka publishes Protobuf messages and dispatches Kafka deliveries to
// typed handlers. Services own business transactions and delivery deduplication.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

type PublisherConfig struct {
	Brokers []string
}

type writer interface {
	WriteMessages(context.Context, ...kafkago.Message) error
	Close() error
}

type Publisher struct{ writer writer }

// NewPublisher validates configuration; connections are established on publish.
func NewPublisher(cfg PublisherConfig) (*Publisher, error) {
	brokers, err := validateBrokers(cfg.Brokers)
	if err != nil {
		return nil, err
	}
	return &Publisher{writer: &kafkago.Writer{
		Addr: kafkago.TCP(brokers...), Balancer: &kafkago.Hash{},
		RequiredAcks: kafkago.RequireAll, WriteTimeout: 5 * time.Second,
		AllowAutoTopicCreation: false,
	}}, nil
}

// Publish waits for acknowledgement. A delivery error can have an uncertain
// outcome; retain the event ID when retrying and make consumers idempotent.
func (p *Publisher) Publish(ctx context.Context, topic string, key []byte, message proto.Message) error {
	if strings.TrimSpace(topic) == "" {
		return errors.New("Kafka topic is required")
	}
	if nilMessage(message) {
		return errors.New("Protobuf message is required")
	}
	payload, err := proto.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode Kafka topic %q: %w", topic, err)
	}
	if err := p.writer.WriteMessages(ctx, kafkago.Message{Topic: topic, Key: key, Value: payload}); err != nil {
		return fmt.Errorf("publish Kafka topic %q: %w", topic, err)
	}
	return nil
}

// Close flushes pending writes and releases connections.
func (p *Publisher) Close() error { return p.writer.Close() }

func nilMessage(message proto.Message) bool {
	return message == nil || (reflect.ValueOf(message).Kind() == reflect.Pointer && reflect.ValueOf(message).IsNil())
}

func validateBrokers(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, errors.New("Kafka brokers are required")
	}
	brokers := make([]string, len(input))
	for i, broker := range input {
		brokers[i] = strings.TrimSpace(broker)
		if brokers[i] == "" {
			return nil, errors.New("Kafka broker must not be empty")
		}
	}
	return brokers, nil
}
