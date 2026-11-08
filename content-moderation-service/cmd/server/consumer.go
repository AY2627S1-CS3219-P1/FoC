package main

import (
	"context"
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
	backoff := minConsumeBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := subscribe(ctx, brokers, store, logger)
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

// Handle processes a review submitted event.
func (h *ingestionHandler) Handle(
	ctx context.Context,
	meta kafka.Metadata,
	event *userseventsv1.ReviewSubmitted,
) error {
	input := jobs.IngestionInput{
		SourceEventID:       uuidFrom(event.EventId),
		ModerationRequestID: uuidFrom(event.ModerationRequestId),
		ReviewID:            uuidFrom(event.ReviewId),
		ReviewerID:          uuidFrom(event.ReviewerId),
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

func uuidFrom(s string) uuid.UUID {
	if s == "" {
		return uuid.Nil
	}
	u, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil
	}
	return u
}
