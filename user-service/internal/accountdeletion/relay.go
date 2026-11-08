package accountdeletion

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database/deletiondb"
	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/outbox"
	"github.com/google/uuid"
)

// factQueries is the subset of the deletion-outbox persistence the relay needs.
// Declaring it here lets the relay be exercised against a fake in unit tests.
type factQueries interface {
	ClaimCommittedFact(
		ctx context.Context,
		arg deletiondb.ClaimCommittedFactParams,
	) (deletiondb.ClaimCommittedFactRow, error)
	MarkFactPublished(ctx context.Context, arg deletiondb.MarkFactPublishedParams) error
	RetryFact(ctx context.Context, arg deletiondb.RetryFactParams) error
}

// Relay publishes committed hard-deletion facts to users.user_deleted. It mirrors
// the user-created relay: claim a fact with a lease, publish, and mark it
// published once the broker acknowledges it, or schedule a durable retry.
type Relay struct {
	queries   factQueries
	publisher outbox.Publisher
	now       func() time.Time
	poll      time.Duration
}

// NewRelay builds the deletion relay. sqlDB is the pool its statements use and
// poll sets how often it looks for committed facts.
func NewRelay(sqlDB *sql.DB, publisher outbox.Publisher, poll time.Duration) *Relay {
	return &Relay{queries: deletiondb.New(sqlDB), publisher: publisher, now: time.Now, poll: poll}
}

// Run loops until the context ends, publishing committed facts once per poll.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(r.poll)
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

// Drain publishes up to limit committed facts. Any deferred error is returned so
// the caller can log it, matching the user-created relay.
func (r *Relay) Drain(ctx context.Context, limit int) error {
	var deferred error
	for range limit {
		if err := ctx.Err(); err != nil {
			return errors.Join(deferred, err)
		}
		now := r.now().UTC()
		fact, err := r.queries.ClaimCommittedFact(ctx, deletiondb.ClaimCommittedFactParams{
			LeaseToken: uuid.New(), LeaseUntil: now.Add(claimLease),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return deferred
		}
		if err != nil {
			return errors.Join(deferred, err)
		}

		message, err := encodeDeleted(fact)
		if err == nil {
			call, cancel := context.WithTimeout(ctx, publishTimeout)
			err = r.publisher.Publish(call, deletedTopic, []byte(fact.UserID.String()), message)
			cancel()
		}
		if err != nil {
			deferred = errors.Join(deferred, err)
			if retryErr := r.queries.RetryFact(ctx, deletiondb.RetryFactParams{
				EventID:     fact.EventID,
				LeaseToken:  fact.LeaseToken.UUID,
				AvailableAt: r.now().UTC().Add(retryDelay),
			}); retryErr != nil {
				return errors.Join(deferred, retryErr)
			}
			continue
		}
		if err := r.queries.MarkFactPublished(ctx, deletiondb.MarkFactPublishedParams{
			EventID:     fact.EventID,
			LeaseToken:  fact.LeaseToken.UUID,
			LeaseNow:    now,
			PublishedAt: now,
		}); err != nil {
			return errors.Join(deferred, err)
		}
	}
	return deferred
}
