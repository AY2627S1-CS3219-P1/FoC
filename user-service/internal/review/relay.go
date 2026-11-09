// The relay delivers committed review submissions to Kafka. It mirrors
// internal/outbox's lease-claim loop; the review repository owns the SQL.
package review

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"google.golang.org/protobuf/proto"
)

const (
	reviewClaimLease     = 30 * time.Second
	reviewPublishTimeout = 5 * time.Second
	reviewRetryDelay     = 2 * time.Second
	reviewPollInterval   = time.Second
	reviewDrainLimit     = 50
)

// OutboxRepository is the relay's persistence surface. Claimed rows hold a
// lease until publication is acknowledged or the relay schedules a retry.
type OutboxRepository interface {
	ClaimSubmittedOutbox(ctx context.Context, eligibleAt, leaseUntil time.Time) (*SubmittedFact, error)
	MarkSubmittedOutboxPublished(ctx context.Context, fact SubmittedFact, now time.Time) error
	ScheduleSubmittedOutboxRetry(ctx context.Context, fact SubmittedFact, availableAt time.Time) error
}

// OutboxPublisher publishes a Protobuf message; *kafka.Publisher satisfies it.
type OutboxPublisher interface {
	Publish(ctx context.Context, topic string, key []byte, message proto.Message) error
}

// OutboxRelay publishes committed review submissions. A publish failure can
// have an uncertain outcome, so the event ID stays stable and the row is
// retried; consumers deduplicate on the event ID.
type OutboxRelay struct {
	repository OutboxRepository
	publisher  OutboxPublisher
	now        func() time.Time
}

func NewOutboxRelay(repository OutboxRepository, publisher OutboxPublisher) *OutboxRelay {
	return &OutboxRelay{repository: repository, publisher: publisher, now: time.Now}
}

func (r *OutboxRelay) Drain(ctx context.Context, limit int) error {
	var deferred error
	eligibleAt := r.now().UTC()
	for range limit {
		if err := ctx.Err(); err != nil {
			return errors.Join(deferred, err)
		}

		now := r.now().UTC()
		fact, err := r.repository.ClaimSubmittedOutbox(ctx, eligibleAt, now.Add(reviewClaimLease))
		if err != nil {
			return errors.Join(deferred, err)
		}
		if fact == nil {
			return deferred
		}

		message, err := decodeSubmittedPayload(fact.PayloadVersion, fact.Payload)
		if err == nil {
			call, cancel := context.WithTimeout(ctx, reviewPublishTimeout)
			err = r.publisher.Publish(call, ReviewSubmittedTopic, []byte(fact.ReviewID.String()), message)
			cancel()
		}
		if err != nil {
			deferred = errors.Join(deferred, err)
			if retryErr := r.repository.ScheduleSubmittedOutboxRetry(
				ctx,
				*fact,
				r.now().UTC().Add(reviewRetryDelay),
			); retryErr != nil {
				return errors.Join(deferred, retryErr)
			}
			continue
		}

		if err := r.repository.MarkSubmittedOutboxPublished(ctx, *fact, r.now().UTC()); err != nil {
			return errors.Join(deferred, err)
		}
	}
	return deferred
}

func (r *OutboxRelay) Run(ctx context.Context) {
	ticker := time.NewTicker(reviewPollInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		if err := r.Drain(ctx, reviewDrainLimit); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "review-submitted outbox publication deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
