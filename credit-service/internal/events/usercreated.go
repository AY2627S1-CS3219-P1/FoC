package events

import (
	"context"
	"errors"
	"log/slog"

	"github.com/AY2627S1-CS3219-P1/FoC/credit-service/internal/account"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	userseventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/users/events/v1"
)

// Opener lists the account operation the users.create handler calls.
type Opener interface {
	Open(ctx context.Context, userID string) error
}

// Users opens an account with the starting credits for each new user.
type Users struct {
	Opener Opener
}

// Handle opens the user's account. The grant is posted only when the account
// is created, so a redelivered event, or one after the user's first Reserve or
// payout, grants nothing (C1.3.1).
func (h *Users) Handle(ctx context.Context, meta kafka.Metadata, event *userseventsv1.UserCreated) error {
	log := slog.With(
		"topic", meta.Topic,
		"partition", meta.Partition,
		"offset", meta.Offset,
		"event_id", event.GetEventId(),
		"user_id", event.GetUserId(),
	)

	err := h.Opener.Open(ctx, event.GetUserId())
	if errors.Is(err, account.ErrInvalidArgument) {
		log.ErrorContext(ctx, "User created event has an invalid user ID; acknowledged without opening an account")
		return nil
	}
	if err != nil {
		return err
	}

	log.InfoContext(ctx, "User account opened")
	return nil
}
