package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

type memoryRepository struct {
	fact        Fact
	availableAt time.Time
	leaseUntil  time.Time
	published   bool
	claimed     int
	retriedAt   time.Time
}

func (r *memoryRepository) Claim(_ context.Context, eligibleAt, leaseUntil time.Time) (*Fact, error) {
	if r.published || r.availableAt.After(eligibleAt) || r.leaseUntil.After(eligibleAt) {
		return nil, nil
	}
	r.claimed++
	r.fact.LeaseToken = uuid.New()
	r.leaseUntil = leaseUntil
	fact := r.fact
	return &fact, nil
}

func (r *memoryRepository) Published(_ context.Context, fact Fact, _ time.Time) error {
	if fact.LeaseToken != r.fact.LeaseToken {
		return errors.New("lease token mismatch")
	}
	r.published = true
	r.leaseUntil = time.Time{}
	return nil
}

func (r *memoryRepository) Retry(_ context.Context, fact Fact, availableAt time.Time) error {
	if fact.LeaseToken != r.fact.LeaseToken {
		return errors.New("lease token mismatch")
	}
	r.availableAt = availableAt
	r.retriedAt = availableAt
	r.leaseUntil = time.Time{}
	return nil
}

type publishCall struct {
	topic string
	key   []byte
	msg   proto.Message
}

type recordingPublisher struct {
	errors []error
	calls  []publishCall
}

func (p *recordingPublisher) Publish(_ context.Context, topic string, key []byte, message proto.Message) error {
	p.calls = append(p.calls, publishCall{topic: topic, key: append([]byte(nil), key...), msg: proto.Clone(message)})
	if len(p.errors) == 0 {
		return nil
	}
	err := p.errors[0]
	p.errors = p.errors[1:]
	return err
}

func TestRelayPublishesUserCreatedAndRetriesWithStableEventID(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	want := Fact{
		EventID:        uuid.New(),
		UserID:         uuid.New(),
		Email:          "new@example.com",
		DisplayName:    "New User",
		CreatedAt:      now.Add(-time.Minute),
		PayloadVersion: 1,
	}
	repository := &memoryRepository{fact: want, availableAt: now.Add(-time.Second)}
	publishErr := errors.New("broker unavailable")
	publisher := &recordingPublisher{errors: []error{publishErr}}
	relay := New(repository, publisher)
	relay.now = func() time.Time { return now }

	if err := relay.Drain(context.Background(), 1); !errors.Is(err, publishErr) {
		t.Fatalf("first drain error = %v, want broker error", err)
	}
	if !repository.retriedAt.Equal(now.Add(retryDelay)) || repository.published {
		t.Fatalf("failed publish was not scheduled for retry: retried_at=%v published=%t", repository.retriedAt, repository.published)
	}

	now = repository.retriedAt
	if err := relay.Drain(context.Background(), 1); err != nil {
		t.Fatalf("retry drain: %v", err)
	}
	if len(publisher.calls) != 2 || !repository.published {
		t.Fatalf("publishes=%d published=%t, want two attempts and acknowledgement", len(publisher.calls), repository.published)
	}
	for _, call := range publisher.calls {
		message, ok := call.msg.(*events.UserCreated)
		if !ok {
			t.Fatalf("published message has type %T, want UserCreated", call.msg)
		}
		if call.topic != topic || string(call.key) != want.UserID.String() {
			t.Fatalf("topic/key = %q/%q, want %q/%q", call.topic, call.key, topic, want.UserID.String())
		}
		if message.GetEventId() != want.EventID.String() || message.GetUserId() != want.UserID.String() ||
			message.GetEmail() != want.Email || message.GetDisplayName() != want.DisplayName ||
			!message.GetCreatedAt().AsTime().Equal(want.CreatedAt) {
			t.Fatalf("published payload = %+v, want fact %+v", message, want)
		}
	}
}

func TestRelayRetriesInvalidFactWithoutPublishing(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	repository := &memoryRepository{
		fact: Fact{EventID: uuid.New(), UserID: uuid.New(), Email: "new@example.com",
			DisplayName: "New User", CreatedAt: now, PayloadVersion: 99},
		availableAt: now.Add(-time.Second),
	}
	publisher := &recordingPublisher{}
	relay := New(repository, publisher)
	relay.now = func() time.Time { return now }

	if err := relay.Drain(context.Background(), 1); err == nil {
		t.Fatal("invalid outbox payload was not reported")
	}
	if len(publisher.calls) != 0 || !repository.retriedAt.Equal(now.Add(retryDelay)) {
		t.Fatalf("invalid payload handling: publishes=%d retry_at=%v", len(publisher.calls), repository.retriedAt)
	}
}

var _ proto.Message = (*events.UserCreated)(nil)
