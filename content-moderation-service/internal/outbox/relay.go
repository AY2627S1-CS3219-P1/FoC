// Package outbox implements the CMS completion relay: claiming rows from the
// transactional outbox and publishing to the moderation.review_moderation_finished
// topic. Events are keyed by review UUID encoded as UTF-8 text so all events for one review
// stay in order; delivery is at least once and republishes use the same
// event_id so downstream consumers can deduplicate.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/database"
	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	moderationevents "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// ErrFenced reports an expired or superseded publication lease.
var ErrFenced = errors.New("outbox lease is no longer owned")

// FinishedTopic is the topic the relay publishes completion events to.
const FinishedTopic = "moderation.review_moderation_finished"

// publisher is the delivery boundary the relay depends on. It mirrors
// *kafka.Publisher so the relay stays testable and decoupled from the concrete
// client.
type publisher interface {
	Publish(ctx context.Context, topic string, key []byte, message proto.Message) error
	Close() error
}

// RelayConfig holds the tunable behaviour of the outbox relay.
type RelayConfig struct {
	PollInterval time.Duration
	Lease        time.Duration
}

// Relay claims unpublished outbox rows and publishes them to Kafka. It leases a
// row atomically so only one instance owns it until it publishes or releases,
// and marks the row published only after successful delivery.
type Relay struct {
	pool      *pgxpool.Pool
	publisher publisher
	cfg       RelayConfig
	logger    *slog.Logger
	workers   sync.WaitGroup
}

// NewRelay injects the database and publisher for completion event delivery.
func NewRelay(pool *pgxpool.Pool, publisher publisher, cfg RelayConfig, logger *slog.Logger) *Relay {
	if cfg.Lease <= 0 {
		cfg.Lease = 30 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Relay{
		pool:      pool,
		publisher: publisher,
		cfg:       cfg,
		logger:    logger,
	}
}

// Start launches the relay goroutine. It claims rows from the outbox, publishes
// them, and marks them as published. On failure the lease is released and the
// row will be retried later. The relay runs until ctx is cancelled.
func (r *Relay) Start(ctx context.Context) {
	r.logger.Info("start outbox relay",
		"poll_interval", r.cfg.PollInterval,
		"lease", r.cfg.Lease,
	)
	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		for {
			if ctx.Err() != nil {
				return
			}

			if err := r.relayOnce(ctx); err != nil {
				if ctx.Err() != nil {
					return
				}
				r.logger.Error("outbox relay failed", "error", err)
			}

			timer := time.NewTimer(r.cfg.PollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
}

// Wait drains the relay after cancellation, then closes its publisher.
func (r *Relay) Wait() {
	r.workers.Wait()
	if err := r.publisher.Close(); err != nil {
		r.logger.Warn("close outbox publisher", "error", err)
	}
}

// relayOnce claims and publishes one outbox row if available. The lease is held
// for the whole operation and released on failure, so a crashed relay can be recovered after expiry. A timed-out broker write may
// still arrive, so redeliveries retain the same event identity.
func (r *Relay) relayOnce(ctx context.Context) error {
	var outboxRow moderationdb.ModerationOutbox
	err := database.Within(ctx, r.pool, func(q *moderationdb.Queries) error {
		row, err := q.ClaimOutboxLease(ctx, r.cfg.Lease.Seconds())
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		outboxRow = row
		return err
	})
	if err != nil {
		return err
	}
	if outboxRow.EventID == uuid.Nil {
		return nil
	}

	// Version 0 was written by pre-release binaries with the same v1 schema.
	if outboxRow.PayloadVersion != 0 && outboxRow.PayloadVersion != 1 {
		return fmt.Errorf("unsupported completion payload version %d", outboxRow.PayloadVersion)
	}
	// The payload is the byte-identical protojson rendered when the row was
	// enqueued, so re-reading it never recomputes a verdict or changes event_id.
	var event moderationevents.ReviewModerationFinished
	if err := protojson.Unmarshal(outboxRow.Payload, &event); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}

	// Key by the review UUID encoded as UTF-8 text so all events for one review land in order.
	reviewID, err := uuid.Parse(event.ReviewId)
	if err != nil {
		return fmt.Errorf("review_id in outbox payload: %w", err)
	}
	key := []byte(reviewID.String())

	// Publish, then fence the published mark on the real lease token so only the
	// owning relay (or one that stole the lease after a crash) can publish.
	publishCtx, cancelPublish := context.WithDeadline(ctx, *outboxRow.LeaseUntil)
	defer cancelPublish()
	if err := r.publisher.Publish(publishCtx, FinishedTopic, key, &event); err != nil {
		return errors.Join(err, r.releaseLease(ctx, outboxRow.EventID, outboxRow.LeaseToken))
	}
	if err := r.markPublished(ctx, outboxRow.EventID, outboxRow.LeaseToken); err != nil {
		return err
	}
	return nil
}

// markPublished marks the outbox row as published, clearing the lease. It is
// fenced on the lease token, so a lost relay cannot mark a row another instance
// is about to republish.
func (r *Relay) markPublished(ctx context.Context, eventID uuid.UUID, leaseToken *uuid.UUID) error {
	return database.Within(ctx, r.pool, func(q *moderationdb.Queries) error {
		if _, err := q.LockOutbox(ctx, eventID); errors.Is(err, pgx.ErrNoRows) {
			return ErrFenced
		} else if err != nil {
			return err
		}
		rows, err := q.MarkOutboxPublished(
			ctx,
			moderationdb.MarkOutboxPublishedParams{EventID: eventID, LeaseToken: leaseToken},
		)
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrFenced
		}
		return nil
	})
}

// releaseLease clears the lease token so a subsequent relay can claim the row
// again and retry delivery.
func (r *Relay) releaseLease(ctx context.Context, eventID uuid.UUID, leaseToken *uuid.UUID) error {
	return database.Within(ctx, r.pool, func(q *moderationdb.Queries) error {
		if _, err := q.LockOutbox(ctx, eventID); errors.Is(err, pgx.ErrNoRows) {
			return ErrFenced
		} else if err != nil {
			return err
		}
		rows, err := q.ReleaseOutboxLease(
			ctx,
			moderationdb.ReleaseOutboxLeaseParams{EventID: eventID, LeaseToken: leaseToken},
		)
		if err != nil {
			return err
		}
		if rows == 0 {
			return ErrFenced
		}
		return nil
	})
}
