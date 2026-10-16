// Package notify sends Telegram messages to order participants when Order
// publishes a status change.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
)

// Topic carries OrderStatusChanged events.
const Topic = "order.status_changed"

// MaxAge is how old an event can be and still be sent. Older events are
// skipped, so a backlog (such as the first start of a new consumer group, which
// reads from the earliest offset) doesn't send outdated updates.
const MaxAge = time.Hour

// Sender is the part of the Bot API the handler needs.
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Links is the part of the linking service the handler needs.
type Links interface {
	Get(ctx context.Context, userID uuid.UUID) (linking.Link, error)
	UnlinkChat(ctx context.Context, chatID int64) error
}

// Deliveries records which recipients already got an event's message, so a
// redelivered event skips them.
type Deliveries interface {
	Sent(ctx context.Context, eventID, userID uuid.UUID) (bool, error)
	RecordSent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error
}

// Handler turns one status change into messages for the participants who
// didn't cause it. Delivery is at least once: a crash between sending and
// recording can repeat one message.
type Handler struct {
	Sender     Sender
	Links      Links
	Deliveries Deliveries
	Log        *slog.Logger
	Now        func() time.Time
}

// Handle implements a pkg/kafka handler for Topic. It returns an error only for
// failures worth replaying: storage errors and transient Telegram errors.
func (h *Handler) Handle(ctx context.Context, _ kafka.Metadata, e *eventsv1.OrderStatusChanged) error {
	log := h.Log.With("event_id", e.GetEventId(), "order_id", e.GetOrderId(), "status", e.GetStatus().String())
	eventID, err := uuid.Parse(e.GetEventId())
	if err != nil {
		// Replaying can't fix a malformed event, so skip it.
		log.Error("skipping order event without a valid event ID")
		return nil
	}
	if age := h.Now().Sub(e.GetOccurredAt().AsTime()); age > MaxAge {
		log.Info("skipping stale order event", "age", age.Round(time.Second).String())
		return nil
	}

	for _, r := range recipients(e) {
		text := message(e, r.role)
		if text == "" {
			continue
		}
		userID, err := uuid.Parse(r.userID)
		if err != nil {
			log.Error("skipping recipient with an invalid user ID", "role", r.role)
			continue
		}
		if err := h.notify(ctx, log, eventID, userID, text); err != nil {
			return err
		}
	}
	return nil
}

func (h *Handler) notify(ctx context.Context, log *slog.Logger, eventID, userID uuid.UUID, text string) error {
	sent, err := h.Deliveries.Sent(ctx, eventID, userID)
	if err != nil {
		return fmt.Errorf("check sent notification: %w", err)
	}
	if sent {
		return nil
	}

	link, err := h.Links.Get(ctx, userID)
	if errors.Is(err, linking.ErrNotLinked) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get Telegram link: %w", err)
	}

	err = h.Sender.SendMessage(ctx, link.ChatID, text)
	var apiErr *telegram.APIError
	switch {
	case err == nil:
	case errors.As(err, &apiErr) && chatGone(apiErr):
		if err := h.Links.UnlinkChat(ctx, link.ChatID); err != nil {
			return fmt.Errorf("unlink unreachable chat: %w", err)
		}
		log.Info("telegram unlinked: chat unreachable", "user_id", userID, "code", apiErr.Code)
		return nil
	case errors.As(err, &apiErr) && apiErr.Code >= 400 && apiErr.Code < 500 && apiErr.Code != http.StatusTooManyRequests:
		// Telegram will reject this message every time; replaying would stall
		// every later notification.
		log.Error("skipping notification Telegram rejected", "user_id", userID, "err", err)
		return nil
	default:
		return fmt.Errorf("send notification: %w", err)
	}

	if err := h.Deliveries.RecordSent(ctx, eventID, userID, h.Now()); err != nil {
		return fmt.Errorf("record sent notification: %w", err)
	}
	return nil
}

// chatGone reports whether the chat can no longer receive messages: the user
// blocked the bot or deleted their account, or the chat doesn't exist.
func chatGone(e *telegram.APIError) bool {
	return e.Code == http.StatusForbidden ||
		(e.Code == http.StatusBadRequest && strings.Contains(strings.ToLower(e.Description), "chat not found"))
}
