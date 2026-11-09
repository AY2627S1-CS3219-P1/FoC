package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/jobs"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
	"github.com/google/uuid"
)

const (
	consumerGroup     = "content-moderation-service"
	minConsumeBackoff = time.Second
	maxConsumeBackoff = 30 * time.Second
)

// consume runs the ingestion subscription until ctx ends. A failed subscriber
// cannot restart, so each failure builds a new one after a growing delay;
// the uncommitted delivery is then redelivered.
func consume(ctx context.Context, brokers []string, store *jobs.JobStore, logger *slog.Logger) {
	consumeWithRetry(ctx, logger, func() error {
		return subscribe(ctx, brokers, store, logger)
	})
}

func consumeWithRetry(ctx context.Context, logger *slog.Logger, runSubscription func() error) {
	backoff := minConsumeBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := runSubscription()
		if ctx.Err() != nil {
			return
		}

		// A subscription that ran for a while was healthy; restart promptly.
		if time.Since(started) > maxConsumeBackoff {
			backoff = minConsumeBackoff
		}
		slog.ErrorContext(ctx, "Kafka subscription stopped; restarting", "error", err, "backoff", backoff)

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		backoff = min(2*backoff, maxConsumeBackoff)
	}
}

func subscribe(ctx context.Context, brokers []string, store *jobs.JobStore, logger *slog.Logger) error {
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{Brokers: brokers, GroupID: consumerGroup})
	if err != nil {
		return err
	}

	// Create the ingestion handler
	handler := &ingestionHandler{store: store}

	err = kafka.Register(subscriber, jobs.UsersReviewSubmittedTopic,
		func() *userseventsv1.ReviewSubmitted { return new(userseventsv1.ReviewSubmitted) },
		handler.Handle,
	)
	if err != nil {
		return err
	}

	return subscriber.Run(ctx)
}

// ingestionHandler adapts the Kafka subscriber interface to the job store.
type ingestionHandler struct {
	store *jobs.JobStore
}

// Handle processes a review submitted event. A required identifier that is
// empty or unparseable is malformed: quarantine the decoded event so the
// partition advances and it is never turned into a zero-UUID job and retried.
func (h *ingestionHandler) Handle(
	ctx context.Context,
	meta kafka.Metadata,
	event *userseventsv1.ReviewSubmitted,
) error {
	sourceEventID, sourceErr := parseEventID(event.EventId)
	moderationRequestID, reqErr := parseEventID(event.ModerationRequestId)
	reviewID, reviewErr := parseEventID(event.ReviewId)
	reviewerID, reviewerErr := parseEventID(event.ReviewerId)
	if sourceErr != nil || reqErr != nil || reviewErr != nil || reviewerErr != nil ||
		event.ContentRevision <= 0 || event.OccurredAt == nil || event.OccurredAt.CheckValid() != nil {
		if qErr := h.store.QuarantineMalformed(ctx, jobs.MalformedInput{
			Topic:      jobs.UsersReviewSubmittedTopic,
			MessageKey: string(meta.Key),
			EventID:    event.EventId,
			Reason:     "missing or malformed submission metadata",
			Comment:    event.Comment,
		}); qErr != nil {
			return qErr
		}
		return nil
	}

	input := jobs.IngestionInput{
		SourceEventID:       sourceEventID,
		ModerationRequestID: moderationRequestID,
		ReviewID:            reviewID,
		ReviewerID:          reviewerID,
		ContentRevision:     event.ContentRevision,
		Comment:             event.Comment,
	}

	result, err := h.store.Ingest(ctx, input)
	if err != nil {
		return err
	}
	slog.DebugContext(ctx, "ingested review submission", "result", result)
	return nil
}

// parseEventID parses a required identifier. An empty or unparseable value is
// an error so the caller quarantines the event rather than storing a zero UUID.
func parseEventID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, fmt.Errorf("empty identifier")
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("invalid identifier")
	}
	return id, nil
}
