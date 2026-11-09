package outbox

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const deletedTopic = "users.user_deleted"

type DeletedFact struct {
	EventID           uuid.UUID
	UserID            uuid.UUID
	DeletionRequestID uuid.UUID
	DeletedAt         time.Time
	PayloadVersion    int32
	LeaseToken        uuid.UUID
}

type DeletedRepository interface {
	Claim(context.Context, time.Time, time.Time) (*DeletedFact, error)
	Published(context.Context, DeletedFact, time.Time) error
	Retry(context.Context, DeletedFact, time.Time) error
}

type DeletedRelay struct {
	repository DeletedRepository
	publisher  Publisher
	now        func() time.Time
}

func NewDeleted(repository DeletedRepository, publisher Publisher) *DeletedRelay {
	return &DeletedRelay{repository: repository, publisher: publisher, now: time.Now}
}

func (r *DeletedRelay) Drain(ctx context.Context, limit int) error {
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

		message, err := encodeDeleted(*fact)
		if err == nil {
			call, cancel := context.WithTimeout(ctx, publishTimeout)
			err = r.publisher.Publish(call, deletedTopic, []byte(fact.UserID.String()), message)
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

func (r *DeletedRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := r.Drain(ctx, defaultDrainLimit); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "user-deleted outbox publication deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
