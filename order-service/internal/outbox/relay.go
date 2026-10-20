// Package outbox publishes committed Order facts without holding database locks
// across Kafka calls. Duplicates retain their original event identities.
package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"
)

type Fact struct {
	ID, OrderID, Kind, LeaseToken, ActorKind, PickupName string
	ActorID                                              *string
	Revision                                             int64
	Version                                              int32
	Payload                                              json.RawMessage
}

type Repository interface {
	Claim(context.Context, time.Time, time.Time) (*Fact, error)
	Published(context.Context, Fact, time.Time) error
	Retry(context.Context, Fact, time.Time) error
}

type Publisher interface {
	Publish(context.Context, string, []byte, proto.Message) error
}

type Relay struct {
	repository Repository
	publisher  Publisher
	now        func() time.Time
}

func New(repository Repository, publisher Publisher) *Relay {
	return &Relay{repository: repository, publisher: publisher, now: time.Now}
}

func (r *Relay) Drain(ctx context.Context, limit int) error {
	var deferred error
	for range limit {
		if err := ctx.Err(); err != nil {
			return errors.Join(deferred, err)
		}
		now := r.now().UTC()
		fact, err := r.repository.Claim(ctx, now, now.Add(30*time.Second))
		if err != nil {
			return errors.Join(deferred, err)
		}
		if fact == nil {
			return deferred
		}
		message, err := encode(*fact)
		if err == nil {
			call, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = r.publisher.Publish(call, fact.Kind, []byte(fact.OrderID), message)
			cancel()
		}
		if err != nil {
			deferred = errors.Join(deferred, err)
			if retryErr := r.repository.Retry(ctx, *fact, r.now().UTC().Add(2*time.Second)); retryErr != nil {
				return errors.Join(deferred, retryErr)
			}
			// This Order remains blocked behind its deferred fact. Other Orders
			// can still use the rest of this bounded batch.
			continue
		}
		// A local failure here deliberately leaves the event outstanding. The next
		// owner republishes the same ID even if Kafka already accepted it.
		if err := r.repository.Published(ctx, *fact, r.now().UTC()); err != nil {
			return errors.Join(deferred, err)
		}
	}
	return deferred
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := r.Drain(ctx, 50); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Order outbox publication deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
