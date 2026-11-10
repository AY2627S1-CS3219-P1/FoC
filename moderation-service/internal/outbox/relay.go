package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/AY2627S1-CS3219-P1/FoC/moderation-service/internal/database/outboxdb"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
)

const (
	leaseTime    = 30 * time.Second
	publishLimit = 5 * time.Second
	retryDelay   = 2 * time.Second
	drainBatch   = 50
)

// Publisher sends one Kafka message.
type Publisher interface {
	Publish(ctx context.Context, topic string, key []byte, message proto.Message) error
}

// Relay publishes recorded outcomes. It leases each row before publishing, so
// several replicas can run it; a lost acknowledgement republishes the same
// event ID, which User Service deduplicates.
type Relay struct {
	queries   *outboxdb.Queries
	publisher Publisher
}

// NewRelay reads the outbox through db.
func NewRelay(db outboxdb.DBTX, publisher Publisher) *Relay {
	return &Relay{queries: outboxdb.New(db), publisher: publisher}
}

// Drain publishes up to limit outcomes.
func (r *Relay) Drain(ctx context.Context, limit int) error {
	eligible := time.Now().UTC()
	var failures []error
	for range limit {
		now, token := time.Now().UTC(), uuid.New()
		row, err := r.queries.ClaimOutcome(ctx, outboxdb.ClaimOutcomeParams{
			LeaseToken: token,
			LeaseUntil: now.Add(leaseTime),
			NowAt:      eligible,
		})
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return errors.Join(append(failures, fmt.Errorf("claim outcome: %w", err))...)
		}

		if err := r.publish(ctx, row); err != nil {
			retryErr := r.queries.RetryOutcome(ctx, outboxdb.RetryOutcomeParams{
				RetryAt:    time.Now().UTC().Add(retryDelay),
				EventID:    row.EventID,
				LeaseToken: token,
			})
			failures = append(failures, err, retryErr)
			continue
		}

		n, err := r.queries.PublishedOutcome(ctx, outboxdb.PublishedOutcomeParams{
			NowAt:      time.Now().UTC(),
			EventID:    row.EventID,
			LeaseToken: token,
		})
		if err != nil {
			return errors.Join(append(failures, fmt.Errorf("mark outcome published: %w", err))...)
		}
		if n != 1 {
			return errors.Join(append(failures, fmt.Errorf("outcome outbox lease lost: %s", row.EventID))...)
		}
	}
	return errors.Join(failures...)
}

func (r *Relay) publish(ctx context.Context, row outboxdb.ClaimOutcomeRow) error {
	event := new(eventsv1.OutcomeRequested)
	if err := proto.Unmarshal(row.Payload, event); err != nil {
		return fmt.Errorf("decode outcome %s: %w", row.EventID, err)
	}
	call, cancel := context.WithTimeout(ctx, publishLimit)
	defer cancel()
	return r.publisher.Publish(call, Topic, []byte(row.MessageKey), event)
}

// Run drains the outbox every second until ctx ends.
func (r *Relay) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := r.Drain(ctx, drainBatch); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "Moderation outcome publication deferred", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
