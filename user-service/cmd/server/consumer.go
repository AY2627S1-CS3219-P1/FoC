package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	moderationeventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/moderation/events/v1"
	review "github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/review"
)

const (
	reviewModerationConsumerGroup = "user-service-review-moderation"
	minConsumeBackoff             = time.Second
	maxConsumeBackoff             = 30 * time.Second
)

// consumeReviewModeration runs User's review moderation subscription until ctx
// ends. A failed subscriber cannot restart, so each failure builds a new one
// after a growing delay; the uncommitted delivery is then redelivered.
func consumeReviewModeration(ctx context.Context, brokers []string, completions *review.CompletionHandler) {
	backoff := minConsumeBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := subscribeReviewModeration(ctx, brokers, completions)
		if ctx.Err() != nil {
			return
		}

		// A subscription that ran for a while was healthy; restart promptly.
		if time.Since(started) > maxConsumeBackoff {
			backoff = minConsumeBackoff
		}
		slog.ErrorContext(ctx, "review moderation subscription stopped; restarting", "error", err, "backoff", backoff)

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

func subscribeReviewModeration(ctx context.Context, brokers []string, completions *review.CompletionHandler) error {
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{
		Brokers: brokers, GroupID: reviewModerationConsumerGroup,
	})
	if err != nil {
		return err
	}

	err = kafka.Register(subscriber, review.ModerationFinishedTopic,
		func() *moderationeventsv1.ReviewModerationFinished {
			return new(moderationeventsv1.ReviewModerationFinished)
		},
		completions.Handle,
	)
	if err != nil {
		return err
	}

	return subscriber.Run(ctx)
}
