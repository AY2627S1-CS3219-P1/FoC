package outbox

import (
	"context"
	"errors"
	"testing"
	"time"

	events "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
)

type deletedMemoryRepository struct {
	fact        DeletedFact
	availableAt time.Time
	leaseUntil  time.Time
	published   bool
	claimed     int
	retriedAt   time.Time
}

func (r *deletedMemoryRepository) Claim(_ context.Context, eligibleAt, leaseUntil time.Time) (*DeletedFact, error) {
	if r.published || r.availableAt.After(eligibleAt) || r.leaseUntil.After(eligibleAt) {
		return nil, nil
	}
	r.claimed++
	r.fact.LeaseToken = uuid.New()
	r.leaseUntil = leaseUntil
	fact := r.fact
	return &fact, nil
}

func (r *deletedMemoryRepository) Published(_ context.Context, fact DeletedFact, _ time.Time) error {
	if fact.LeaseToken != r.fact.LeaseToken {
		return errors.New("lease token mismatch")
	}
	r.published = true
	r.leaseUntil = time.Time{}
	return nil
}

func (r *deletedMemoryRepository) Retry(_ context.Context, fact DeletedFact, availableAt time.Time) error {
	if fact.LeaseToken != r.fact.LeaseToken {
		return errors.New("lease token mismatch")
	}
	r.availableAt = availableAt
	r.retriedAt = availableAt
	r.leaseUntil = time.Time{}
	return nil
}

func TestDeletedRelayRetriesTheSameCommittedIdentity(t *testing.T) {
	now := time.Date(2026, 11, 9, 3, 0, 0, 0, time.UTC)
	fact := DeletedFact{
		EventID: uuid.New(), UserID: uuid.New(), DeletionRequestID: uuid.New(),
		DeletedAt: now.Add(-time.Minute), PayloadVersion: 1,
	}
	repository := &deletedMemoryRepository{fact: fact}
	brokerError := errors.New("broker unavailable")
	publisher := &recordingPublisher{errors: []error{brokerError}}
	relay := NewDeleted(repository, publisher)
	relay.now = func() time.Time { return now }
	if err := relay.Drain(context.Background(), 1); !errors.Is(err, brokerError) || repository.published {
		t.Fatalf("failed delivery was acknowledged: published=%v err=%v", repository.published, err)
	}
	now = repository.retriedAt
	if err := relay.Drain(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if len(publisher.calls) != 2 || !repository.published {
		t.Fatal("committed deletion was not retried and acknowledged")
	}
	for _, call := range publisher.calls {
		message, ok := call.msg.(*events.UserDeleted)
		if !ok {
			t.Fatalf("wrong event type: %T", call.msg)
		}
		if call.topic != "users.user_deleted" || string(call.key) != fact.UserID.String() ||
			message.EventId != fact.EventID.String() || message.UserId != fact.UserID.String() ||
			message.DeletionRequestId != fact.DeletionRequestID.String() || !message.DeletedAt.AsTime().Equal(fact.DeletedAt) {
			t.Fatalf("retry changed committed deletion identity: %+v", call)
		}
	}
}

func TestDeletedEncoderRejectsIncompleteFacts(t *testing.T) {
	if _, err := encodeDeleted(DeletedFact{PayloadVersion: 1}); err == nil {
		t.Fatal("accepted incomplete deletion event")
	}
}

