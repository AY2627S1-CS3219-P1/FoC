package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/events"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	ordereventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
)

const (
	consumerGroup     = "credit-service"
	minConsumeBackoff = time.Second
	maxConsumeBackoff = 30 * time.Second
)

// consume runs Credit's subscription until ctx ends. A failed subscriber
// cannot restart, so each failure builds a new one after a growing delay;
// the uncommitted delivery is then redelivered.
func consume(ctx context.Context, brokers []string, outcomes *events.Outcomes, users *events.Users) {
	backoff := minConsumeBackoff
	for ctx.Err() == nil {
		started := time.Now()
		err := subscribe(ctx, brokers, outcomes, users)
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

func subscribe(ctx context.Context, brokers []string, outcomes *events.Outcomes, users *events.Users) error {
	subscriber, err := kafka.NewSubscriber(kafka.SubscriberConfig{Brokers: brokers, GroupID: consumerGroup})
	if err != nil {
		return err
	}

	err = kafka.Register(subscriber, events.OrderOutcomeTopic,
		func() *ordereventsv1.OrderOutcome { return new(ordereventsv1.OrderOutcome) },
		outcomes.Handle,
	)
	if err != nil {
		return err
	}
	err = kafka.Register(subscriber, events.UserCreatedTopic,
		func() *userseventsv1.UserCreated { return new(userseventsv1.UserCreated) },
		users.Handle,
	)
	if err != nil {
		return err
	}

	return subscriber.Run(ctx)
}
