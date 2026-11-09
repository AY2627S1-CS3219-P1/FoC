package kafka

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

var errTest = errors.New("test failure")

type fakeWriter struct {
	messages []kafkago.Message
	err      error
	closed   bool
}

func (w *fakeWriter) WriteMessages(_ context.Context, messages ...kafkago.Message) error {
	w.messages = append(w.messages, messages...)
	return w.err
}
func (w *fakeWriter) Close() error { w.closed = true; return w.err }

func TestPublish(t *testing.T) {
	w := &fakeWriter{}
	p := &Publisher{writer: w}
	if err := p.Publish(
		context.Background(),
		"users.create",
		[]byte("user-id"),
		wrapperspb.String("created"),
	); err != nil {
		t.Fatal(err)
	}
	m := w.messages[0]
	var value wrapperspb.StringValue
	if err := proto.Unmarshal(m.Value, &value); err != nil {
		t.Fatal(err)
	}
	if m.Topic != "users.create" || string(m.Key) != "user-id" || value.Value != "created" {
		t.Fatalf("unexpected message: %+v", m)
	}
	w.err = errTest
	if err := p.Publish(
		context.Background(),
		"users.create",
		nil,
		wrapperspb.String("created"),
	); !errors.Is(
		err,
		errTest,
	) {
		t.Fatalf("write error: %v", err)
	}
	if err := p.Close(); !errors.Is(err, errTest) || !w.closed {
		t.Fatalf("close: %v", err)
	}
}

func TestPublishRejectsInvalidMessages(t *testing.T) {
	var typedNil *wrapperspb.StringValue
	for _, tc := range []struct {
		name, topic string
		message     proto.Message
	}{
		{"blank topic", " ", wrapperspb.String("ok")},
		{"nil", "topic", nil}, {"typed nil", "topic", typedNil},
		{"invalid UTF8", "topic", wrapperspb.String(string([]byte{0xff}))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &fakeWriter{}
			if err := (&Publisher{writer: w}).Publish(context.Background(), tc.topic, nil, tc.message); err == nil {
				t.Fatal("expected error")
			}
			if len(w.messages) != 0 {
				t.Fatal("invalid message was written")
			}
		})
	}
}

func TestConfiguration(t *testing.T) {
	for _, brokers := range [][]string{nil, {""}, {"broker:9092", " "}} {
		if _, err := NewPublisher(PublisherConfig{Brokers: brokers}); err == nil {
			t.Fatal("accepted invalid brokers")
		}
		if _, err := NewSubscriber(SubscriberConfig{Brokers: brokers, GroupID: "group"}); err == nil {
			t.Fatal("accepted invalid brokers")
		}
	}
	if _, err := NewSubscriber(SubscriberConfig{Brokers: []string{"broker:9092"}}); err == nil {
		t.Fatal("accepted empty group")
	}
	p, err := NewPublisher(PublisherConfig{Brokers: []string{" broker:9092 "}})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := p.Close(); err != nil {
			t.Errorf("close publisher: %v", err)
		}
	}()
	w := p.writer.(*kafkago.Writer)
	if w.RequiredAcks != kafkago.RequireAll || w.Async || w.AllowAutoTopicCreation || w.WriteTimeout != 5*time.Second {
		t.Fatal("incorrect writer settings")
	}
	if _, ok := w.Balancer.(*kafkago.Hash); !ok {
		t.Fatal("expected key hashing")
	}
}

type fakeReader struct {
	messages                      []kafkago.Message
	commits                       []kafkago.Message
	fetchErr, commitErr, closeErr error
	closed                        bool
	onCommit                      func()
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	if r.fetchErr != nil {
		return kafkago.Message{}, r.fetchErr
	}
	if len(r.messages) != 0 {
		m := r.messages[0]
		r.messages = r.messages[1:]
		return m, nil
	}
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}
func (r *fakeReader) CommitMessages(_ context.Context, messages ...kafkago.Message) error {
	if r.commitErr != nil {
		return r.commitErr
	}
	r.commits = append(r.commits, messages...)
	if r.onCommit != nil {
		r.onCommit()
	}
	return nil
}
func (r *fakeReader) Close() error { r.closed = true; return r.closeErr }

func testSubscriber(t *testing.T, r *fakeReader) *Subscriber {
	t.Helper()
	s, err := NewSubscriber(SubscriberConfig{Brokers: []string{"broker:9092"}, GroupID: "group"})
	if err != nil {
		t.Fatal(err)
	}
	s.newReader = func(cfg kafkago.ReaderConfig) reader {
		if cfg.GroupID != "group" || cfg.CommitInterval != 0 || cfg.StartOffset != kafkago.FirstOffset ||
			!reflect.DeepEqual(cfg.GroupTopics, []string{"topic"}) {
			t.Fatalf("reader settings: %+v", cfg)
		}
		return r
	}
	return s
}

func registerString(
	t *testing.T,
	s *Subscriber,
	handler func(context.Context, Metadata, *wrapperspb.StringValue) error,
) {
	t.Helper()
	if err := Register(
		s,
		"topic",
		func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) },
		handler,
	); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchAndCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, _ := proto.Marshal(wrapperspb.String("first"))
	second, _ := proto.Marshal(wrapperspb.String("second"))
	r := &fakeReader{messages: []kafkago.Message{
		{Topic: "topic", Partition: 2, Offset: 10, Key: []byte("id"), Value: first, Time: time.Unix(123, 0)},
		{Topic: "topic", Partition: 2, Offset: 11, Value: second},
	}}
	r.onCommit = func() {
		if len(r.commits) == 2 {
			cancel()
		}
	}
	s := testSubscriber(t, r)
	var seen []*wrapperspb.StringValue
	registerString(t, s, func(_ context.Context, meta Metadata, value *wrapperspb.StringValue) error {
		if len(r.commits) != len(seen) {
			t.Fatal("commit occurred before handler")
		}
		if len(seen) == 0 &&
			(meta.Topic != "topic" || meta.Partition != 2 || meta.Offset != 10 || string(meta.Key) != "id" || !meta.Timestamp.Equal(time.Unix(123, 0))) {
			t.Fatalf("metadata: %+v", meta)
		}
		seen = append(seen, value)
		return nil
	})
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(r.commits) != 2 || !r.closed || len(seen) != 2 || seen[0] == seen[1] || seen[0].Value != "first" ||
		seen[1].Value != "second" {
		t.Fatal("incorrect dispatch/cleanup")
	}
	if err := s.Run(ctx); err == nil {
		t.Fatal("accepted second Run")
	}
	if err := Register(
		s,
		"other",
		func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) },
		func(context.Context, Metadata, *wrapperspb.StringValue) error { return nil },
	); err == nil {
		t.Fatal("registered after startup")
	}
}

func TestSubscriberFailures(t *testing.T) {
	payload, _ := proto.Marshal(wrapperspb.String("ok"))
	for _, failure := range []string{"decode", "handler", "commit", "fetch", "close", "unregistered"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			r := &fakeReader{messages: []kafkago.Message{{Topic: "topic", Partition: 3, Offset: 7, Value: payload}}}
			switch failure {
			case "decode":
				r.messages[0].Value = []byte{0xff}
			case "commit":
				r.commitErr = errTest
			case "fetch":
				r.fetchErr = errTest
			case "close":
				r.closeErr = errTest
				r.onCommit = cancel
			case "unregistered":
				r.messages[0].Topic = "other"
			}
			s := testSubscriber(t, r)
			called := false
			registerString(t, s, func(context.Context, Metadata, *wrapperspb.StringValue) error {
				called = true
				if failure == "handler" {
					return errTest
				}
				return nil
			})
			err := s.Run(ctx)
			if err == nil || !r.closed {
				t.Fatalf("failure not returned or reader not closed: %v", err)
			}
			if failure != "close" && len(r.commits) != 0 {
				t.Fatal("committed failed message")
			}
			if failure == "decode" && called {
				t.Fatal("handler received malformed message")
			}
			if failure == "handler" || failure == "commit" || failure == "fetch" || failure == "close" {
				if !errors.Is(err, errTest) {
					t.Fatalf("lost underlying error: %v", err)
				}
			}
			if failure == "handler" || failure == "commit" || failure == "decode" {
				if !strings.Contains(err.Error(), "partition 3 offset 7") {
					t.Fatalf("missing delivery context: %v", err)
				}
			}
		})
	}
}

func TestRegistrationValidation(t *testing.T) {
	s := testSubscriber(t, &fakeReader{})
	factory := func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) }
	handler := func(context.Context, Metadata, *wrapperspb.StringValue) error { return nil }
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("started without handlers")
	}
	for _, err := range []error{
		Register(nil, "topic", factory, handler), Register(s, " ", factory, handler),
		Register(s, "topic", nil, handler), Register(s, "topic", factory, nil),
		Register(s, "topic", func() *wrapperspb.StringValue { return nil }, handler),
	} {
		if err == nil {
			t.Fatal("accepted invalid registration")
		}
	}
	registerString(t, s, handler)
	if err := Register(s, "topic", factory, handler); err == nil {
		t.Fatal("accepted duplicate topic")
	}
}

func TestCancellation(t *testing.T) {
	r := &fakeReader{}
	s := testSubscriber(t, r)
	registerString(t, s, func(context.Context, Metadata, *wrapperspb.StringValue) error { return nil })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := s.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !r.closed || len(r.commits) != 0 {
		t.Fatal("incorrect cancellation cleanup")
	}
}

func TestCancelledHandlerDoesNotCommit(t *testing.T) {
	payload, _ := proto.Marshal(wrapperspb.String("ok"))
	for _, handlerError := range []error{context.Canceled, errTest} {
		ctx, cancel := context.WithCancel(context.Background())
		r := &fakeReader{messages: []kafkago.Message{{Topic: "topic", Value: payload}}}
		s := testSubscriber(t, r)
		registerString(t, s, func(context.Context, Metadata, *wrapperspb.StringValue) error {
			cancel()
			return handlerError
		})
		err := s.Run(ctx)
		if handlerError == context.Canceled && err != nil {
			t.Fatalf("cancellation should stop normally: %v", err)
		}
		if handlerError == errTest && !errors.Is(err, errTest) {
			t.Fatalf("cancellation hid a handler failure: %v", err)
		}
		if !r.closed || len(r.commits) != 0 {
			t.Fatal("cancelled handler committed or failed to close")
		}
	}
}

func TestFactoryReturningNilDuringDelivery(t *testing.T) {
	r := &fakeReader{messages: []kafkago.Message{{Topic: "topic"}}}
	s := testSubscriber(t, r)
	calls := 0
	err := Register(s, "topic", func() *wrapperspb.StringValue {
		calls++
		if calls > 1 {
			return nil
		}
		return new(wrapperspb.StringValue)
	}, func(context.Context, Metadata, *wrapperspb.StringValue) error {
		t.Fatal("handler should not receive nil")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("accepted nil factory result")
	}
	if len(r.commits) != 0 || !r.closed {
		t.Fatal("incorrect factory failure cleanup")
	}
}

func TestMultipleTopicsAndConcurrentStartup(t *testing.T) {
	s, err := NewSubscriber(SubscriberConfig{Brokers: []string{"broker:9092"}, GroupID: "group"})
	if err != nil {
		t.Fatal(err)
	}
	handler := func(context.Context, Metadata, *wrapperspb.StringValue) error { return nil }
	factory := func() *wrapperspb.StringValue { return new(wrapperspb.StringValue) }
	for _, topic := range []string{"z", "a"} {
		if err := Register(s, topic, factory, handler); err != nil {
			t.Fatal(err)
		}
	}
	started := make(chan kafkago.ReaderConfig, 1)
	r := &fakeReader{}
	s.newReader = func(cfg kafkago.ReaderConfig) reader { started <- cfg; return r }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	cfg := <-started
	if !reflect.DeepEqual(cfg.GroupTopics, []string{"a", "z"}) {
		t.Fatalf("topics: %v", cfg.GroupTopics)
	}
	if err := Register(s, "other", factory, handler); err == nil {
		t.Fatal("registered during Run")
	}
	if err := s.Run(ctx); err == nil {
		t.Fatal("started concurrently")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !r.closed {
		t.Fatal("reader was not closed")
	}
}
