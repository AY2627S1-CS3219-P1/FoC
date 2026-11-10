package review

import (
	"context"
	"errors"
	"testing"
	"time"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Emulates claim expiry so retry tests exercise the relay lease lifecycle.
type fakeOutboxRepository struct {
	fact        SubmittedFact
	availableAt time.Time
	leaseUntil  time.Time
	published   bool
	retriedAt   time.Time
	claimErr    error
	publishErr  error
	retryErr    error
}

func (r *fakeOutboxRepository) ClaimSubmittedOutbox(
	_ context.Context, eligibleAt, leaseUntil time.Time,
) (*SubmittedFact, error) {
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	if r.published || r.availableAt.After(eligibleAt) || r.leaseUntil.After(eligibleAt) {
		return nil, nil
	}
	r.fact.LeaseToken = uuid.New()
	r.leaseUntil = leaseUntil
	fact := r.fact
	return &fact, nil
}

func (r *fakeOutboxRepository) MarkSubmittedOutboxPublished(_ context.Context, fact SubmittedFact, _ time.Time) error {
	if fact.LeaseToken != r.fact.LeaseToken {
		return errors.New("lease token mismatch")
	}
	if r.publishErr != nil {
		return r.publishErr
	}
	r.published = true
	r.leaseUntil = time.Time{}
	return nil
}

func (r *fakeOutboxRepository) ScheduleSubmittedOutboxRetry(
	_ context.Context, fact SubmittedFact, availableAt time.Time,
) error {
	if fact.LeaseToken != r.fact.LeaseToken {
		return errors.New("lease token mismatch")
	}
	if r.retryErr != nil {
		return r.retryErr
	}
	r.availableAt = availableAt
	r.retriedAt = availableAt
	r.leaseUntil = time.Time{}
	return nil
}

type recordingOutboxPublisher struct {
	errs  []error
	calls []struct {
		topic string
		key   []byte
		msg   proto.Message
	}
}

func (p *recordingOutboxPublisher) Publish(_ context.Context, topic string, key []byte, message proto.Message) error {
	p.calls = append(p.calls, struct {
		topic string
		key   []byte
		msg   proto.Message
	}{topic: topic, key: append([]byte(nil), key...), msg: proto.Clone(message)})
	if len(p.errs) == 0 {
		return nil
	}
	err := p.errs[0]
	p.errs = p.errs[1:]
	return err
}

func reviewSubmittedFact(t *testing.T) SubmittedFact {
	t.Helper()
	reviewID := uuid.New()
	eventID := uuid.New()
	payload, err := encodeSubmittedPayload(&events.ReviewSubmitted{
		EventId:             eventID.String(),
		ModerationRequestId: uuid.New().String(),
		ReviewId:            reviewID.String(),
		ReviewerId:          uuid.New().String(),
		ContentRevision:     1,
		Comment:             "pending review",
		OccurredAt:          timestamppb.New(time.Date(2026, 11, 1, 11, 0, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return SubmittedFact{
		EventID:        eventID,
		ReviewID:       reviewID,
		PayloadVersion: submittedPayloadVersion,
		Payload:        payload,
	}
}

func TestOutboxRelayPublishesAndAcknowledges(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	want := reviewSubmittedFact(t)
	repository := &fakeOutboxRepository{fact: want, availableAt: now.Add(-time.Second)}
	publisher := &recordingOutboxPublisher{}
	relay := NewOutboxRelay(repository, publisher)
	relay.now = func() time.Time { return now }

	if err := relay.Drain(context.Background(), 1); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if !repository.published || len(publisher.calls) != 1 {
		t.Fatalf(
			"published=%t publishes=%d, want acknowledgement after one publish",
			repository.published,
			len(publisher.calls),
		)
	}
	call := publisher.calls[0]
	if call.topic != ReviewSubmittedTopic {
		t.Fatalf("topic = %q, want %q", call.topic, ReviewSubmittedTopic)
	}
	if string(call.key) != want.ReviewID.String() {
		t.Fatalf("key = %q, want the review id %q", call.key, want.ReviewID)
	}
	message, ok := call.msg.(*events.ReviewSubmitted)
	if !ok {
		t.Fatalf("published message has type %T", call.msg)
	}
	if message.GetEventId() != want.EventID.String() || message.GetComment() != "pending review" ||
		message.GetContentRevision() != 1 {
		t.Fatalf("published payload = %+v, want fact %+v", message, want)
	}
}

func TestOutboxRelayRetriesFailedPublishWithStableEventID(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	want := reviewSubmittedFact(t)
	repository := &fakeOutboxRepository{fact: want, availableAt: now.Add(-time.Second)}
	publishErr := errors.New("broker unavailable")
	publisher := &recordingOutboxPublisher{errs: []error{publishErr}}
	relay := NewOutboxRelay(repository, publisher)
	relay.now = func() time.Time { return now }

	if err := relay.Drain(context.Background(), 1); !errors.Is(err, publishErr) {
		t.Fatalf("first drain error = %v, want broker error", err)
	}
	if repository.published || !repository.retriedAt.Equal(now.Add(reviewRetryDelay)) {
		t.Fatalf(
			"failed publish was not scheduled for retry: retried_at=%v published=%t",
			repository.retriedAt,
			repository.published,
		)
	}

	repository.availableAt = repository.retriedAt
	relay.now = func() time.Time { return repository.retriedAt }
	if err := relay.Drain(context.Background(), 1); err != nil {
		t.Fatalf("retry drain: %v", err)
	}
	if len(publisher.calls) != 2 || !repository.published {
		t.Fatalf(
			"publishes=%d published=%t, want two attempts and acknowledgement",
			len(publisher.calls),
			repository.published,
		)
	}
	first := publisher.calls[0].msg.(*events.ReviewSubmitted)
	second := publisher.calls[1].msg.(*events.ReviewSubmitted)
	if first.GetEventId() != second.GetEventId() {
		t.Fatalf("event id changed across retries: %q then %q", first.GetEventId(), second.GetEventId())
	}
}

func TestOutboxRelayRetriesInvalidPayloadWithoutPublishing(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	repository := &fakeOutboxRepository{
		fact: SubmittedFact{
			EventID:        uuid.New(),
			ReviewID:       uuid.New(),
			PayloadVersion: 99,
			Payload:        []byte(`{}`),
		},
		availableAt: now.Add(-time.Second),
	}
	publisher := &recordingOutboxPublisher{}
	relay := NewOutboxRelay(repository, publisher)
	relay.now = func() time.Time { return now }

	if err := relay.Drain(context.Background(), 1); err == nil {
		t.Fatal("invalid outbox payload was not reported")
	}
	if len(publisher.calls) != 0 || !repository.retriedAt.Equal(now.Add(reviewRetryDelay)) {
		t.Fatalf("invalid payload handling: publishes=%d retry_at=%v", len(publisher.calls), repository.retriedAt)
	}
}
