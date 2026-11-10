// Package outbox publishes committed results at least once, preserving event IDs on retries.
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

const FinishedTopic = "moderation.review_moderation_finished"

type publisher interface {
	Publish(ctx context.Context, topic string, key []byte, message proto.Message) error
	Close() error
}

type RelayConfig struct {
	PollInterval time.Duration
	Lease        time.Duration
}

// Mark delivery only after broker acknowledgement; ambiguous publication can redeliver.
type Relay struct {
	pool      *pgxpool.Pool
	publisher publisher
	cfg       RelayConfig
	logger    *slog.Logger
	workers   sync.WaitGroup
}

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

// Hold the lease through publication; preserve event identity after ambiguous writes.
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

	if outboxRow.PayloadVersion != 1 {
		return fmt.Errorf("unsupported completion payload version %d", outboxRow.PayloadVersion)
	}
	var event moderationevents.ReviewModerationFinished
	if err := protojson.Unmarshal(outboxRow.Payload, &event); err != nil {
		return fmt.Errorf("decode outbox payload: %w", err)
	}

	reviewID, err := uuid.Parse(event.ReviewId)
	if err != nil {
		return fmt.Errorf("review_id in outbox payload: %w", err)
	}
	key := []byte(reviewID.String())

	// Publication can succeed even if the fenced acknowledgement fails; consumers deduplicate.
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

// A stale relay cannot acknowledge another owner's row.
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
