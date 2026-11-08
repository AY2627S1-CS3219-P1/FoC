// Package outbox implements the CMS completion relay: claiming rows from the
// transactional outbox and publishing to the moderation.review_moderation_finished
// topic. Events are keyed by review UUID bytes so all events for one review
// stay in order; delivery is at least once and republishes use the same
// event_id so downstream consumers can deduplicate.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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

// Relay claims published-outbox rows and publishes them to Kafka. It leases a
// row atomically so only one instance owns it until it publishes or releases,
// and marks the row published only after successful delivery.
type Relay struct {
	pool      *pgxpool.Pool
	publisher publisher
	cfg       RelayConfig
	logger    *slog.Logger
}

// NewRelay constructs the outbox relay. The publisher is the only dependency;
// it must be able to publish to the moderation.review_moderation_finished topic.
func NewRelay(pool *pgxpool.Pool, publisher publisher, cfg RelayConfig, logger *slog.Logger) *Relay {
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
	go func() {
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

			time.Sleep(r.cfg.PollInterval)
		}
	}()
}

// relayOnce claims and publishes one outbox row if available. The lease is held
// for the whole operation and released on failure, so a crashed relay never
// leaves a row owned by nobody and never publishes a row another instance owns.
func (r *Relay) relayOnce(ctx context.Context) error {
	var outboxRow moderationdb.ModerationOutbox
	err := database.Within(ctx, r.pool, func(q *moderationdb.Queries) error {
		row, err := q.ClaimOutboxLease(ctx)
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

	// The payload is the byte-identical protojson rendered when the row was
	// enqueued, so re-reading it never recomputes a verdict or changes event_id.
	var event moderationevents.ReviewModerationFinished
	if err := protojson.Unmarshal(outboxRow.Payload, &event); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}

	// Key by the review UUID bytes so all events for one review land in order.
	reviewID, err := uuid.Parse(event.ReviewId)
	if err != nil {
		return fmt.Errorf("review_id in outbox payload: %w", err)
	}
	key := reviewID[:]

	// Publish, then fence the published mark on the real lease token so only the
	// owning relay (or one that stole the lease after a crash) can publish.
	if err := r.publisher.Publish(ctx, FinishedTopic, key, &event); err != nil {
		return r.releaseLease(ctx, outboxRow.EventID, outboxRow.LeaseToken)
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
	_, err := moderationdb.New(r.pool).MarkOutboxPublished(ctx, moderationdb.MarkOutboxPublishedParams{
		EventID:    eventID,
		LeaseToken: leaseToken,
	})
	return err
}

// releaseLease clears the lease token so a subsequent relay can claim the row
// again and retry delivery.
func (r *Relay) releaseLease(ctx context.Context, eventID uuid.UUID, leaseToken *uuid.UUID) error {
	_, err := moderationdb.New(r.pool).ReleaseOutboxLease(ctx, moderationdb.ReleaseOutboxLeaseParams{
		EventID:    eventID,
		LeaseToken: leaseToken,
	})
	return err
}
