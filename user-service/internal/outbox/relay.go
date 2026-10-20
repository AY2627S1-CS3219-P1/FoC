// Package outbox delivers committed registration facts to Kafka.
package outbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	topic             = "users.create"
	claimLease        = 30 * time.Second
	publishTimeout    = 5 * time.Second
	retryDelay        = 2 * time.Second
	pollInterval      = time.Second
	defaultDrainLimit = 50
)

type Fact struct {
	EventID        uuid.UUID
	UserID         uuid.UUID
	Email          string
	DisplayName    string
	CreatedAt      time.Time
	PayloadVersion int32
	LeaseToken     uuid.UUID
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
	eligibleAt := r.now().UTC()
	for range limit {
		if err := ctx.Err(); err != nil {
			return errors.Join(deferred, err)
		}

		now := r.now().UTC()
		fact, err := r.repository.Claim(ctx, eligibleAt, now.Add(claimLease))
		if err != nil {
			return errors.Join(deferred, err)
		}
		if fact == nil {
			return deferred
		}

		message, err := encode(*fact)
		if err == nil {
			call, cancel := context.WithTimeout(ctx, publishTimeout)
			err = r.publisher.Publish(call, topic, []byte(fact.UserID.String()), message)
			cancel()
		}
		if err != nil {
			deferred = errors.Join(deferred, err)
			if retryErr := r.repository.Retry(ctx, *fact, r.now().UTC().Add(retryDelay)); retryErr != nil {
				return errors.Join(deferred, retryErr)
			}
			continue
		}

		if err := r.repository.Published(ctx, *fact, r.now().UTC()); err != nil {
			return errors.Join(deferred, err)
		}
	}
	return deferred
}

func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := r.Drain(ctx, defaultDrainLimit); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "user-created outbox publication deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
