package kafka

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

type SubscriberConfig struct {
	Brokers []string
	GroupID string
}

type Metadata struct {
	Topic     string
	Key       []byte
	Partition int
	Offset    int64
	Timestamp time.Time
}

type reader interface {
	FetchMessage(context.Context) (kafkago.Message, error)
	CommitMessages(context.Context, ...kafkago.Message) error
	Close() error
}

type dispatch func(context.Context, kafkago.Message) error

type Subscriber struct {
	mu        sync.Mutex
	cfg       SubscriberConfig
	handlers  map[string]dispatch
	started   bool
	newReader func(kafkago.ReaderConfig) reader
}

func NewSubscriber(cfg SubscriberConfig) (*Subscriber, error) {
	brokers, err := validateBrokers(cfg.Brokers)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.GroupID) == "" {
		return nil, errors.New("Kafka consumer group ID is required")
	}
	cfg.Brokers = brokers
	return &Subscriber{cfg: cfg, handlers: make(map[string]dispatch),
		newReader: func(cfg kafkago.ReaderConfig) reader { return kafkago.NewReader(cfg) }}, nil
}

// Register binds a topic to a typed handler. The factory must return a fresh,
// non-nil message on every call. Register all topics before starting Run.
func Register[T proto.Message](
	s *Subscriber,
	topic string,
	factory func() T,
	handler func(context.Context, Metadata, T) error,
) error {
	if s == nil {
		return errors.New("Kafka subscriber is required")
	}
	if strings.TrimSpace(topic) == "" {
		return errors.New("Kafka topic is required")
	}
	if factory == nil || handler == nil {
		return errors.New("Kafka message factory and handler are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return errors.New("cannot register after Kafka subscriber starts")
	}
	if _, exists := s.handlers[topic]; exists {
		return fmt.Errorf("Kafka topic %q already registered", topic)
	}
	if nilMessage(factory()) {
		return errors.New("Kafka message factory returned nil")
	}
	s.handlers[topic] = func(ctx context.Context, delivery kafkago.Message) error {
		message := factory()
		if nilMessage(message) {
			return errors.New("Kafka message factory returned nil")
		}
		if err := proto.Unmarshal(delivery.Value, message); err != nil {
			return fmt.Errorf("decode Protobuf: %w", err)
		}
		return handler(ctx, Metadata{Topic: delivery.Topic, Key: delivery.Key,
			Partition: delivery.Partition, Offset: delivery.Offset, Timestamp: delivery.Time}, message)
	}
	return nil
}

// Run processes deliveries sequentially and commits only after handler success.
// It may be called once. Failures stop the subscription; restart with a new
// Subscriber to replay uncommitted messages. Cancellation shuts down normally.
func (s *Subscriber) Run(ctx context.Context) (runErr error) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("Kafka subscriber already started")
	}
	if len(s.handlers) == 0 {
		s.mu.Unlock()
		return errors.New("Kafka subscriber has no handlers")
	}
	s.started = true
	topics := make([]string, 0, len(s.handlers))
	for topic := range s.handlers {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	s.mu.Unlock()
	if ctx.Err() != nil {
		return nil
	}
	r := s.newReader(kafkago.ReaderConfig{
		Brokers: s.cfg.Brokers, GroupID: s.cfg.GroupID, GroupTopics: topics,
		StartOffset: kafkago.FirstOffset, CommitInterval: 0,
	})
	defer func() {
		if err := r.Close(); err != nil {
			runErr = errors.Join(runErr, fmt.Errorf("close Kafka subscriber: %w", err))
		}
	}()
	for ctx.Err() == nil {
		delivery, err := r.FetchMessage(ctx)
		if err != nil {
			if cancelled(ctx, err) {
				return nil
			}
			return fmt.Errorf("fetch Kafka group %q: %w", s.cfg.GroupID, err)
		}
		handle, exists := s.handlers[delivery.Topic]
		if !exists {
			return deliveryError("dispatch", delivery, errors.New("no registered handler"))
		}
		if err := handle(ctx, delivery); err != nil {
			if cancelled(ctx, err) {
				return nil
			}
			return deliveryError("handle", delivery, err)
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := r.CommitMessages(ctx, delivery); err != nil {
			if cancelled(ctx, err) {
				return nil
			}
			return deliveryError("commit", delivery, err)
		}
	}
	return nil
}

func cancelled(ctx context.Context, err error) bool {
	return ctx.Err() != nil && errors.Is(err, ctx.Err())
}

func deliveryError(action string, message kafkago.Message, err error) error {
	return fmt.Errorf(
		"%s Kafka topic %q partition %d offset %d: %w",
		action,
		message.Topic,
		message.Partition,
		message.Offset,
		err,
	)
}
