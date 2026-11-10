// Package notify messages order participants when Order publishes a status
// change: by Telegram, or by email when Telegram can't reach them.
package notify

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/kafka"
	eventsv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/kafkagen/order/events/v1"
	"github.com/google/uuid"
)

// Topic carries OrderStatusChanged events.
const Topic = "order.status_changed"

// OfferTopic carries OrderOfferMade events.
const OfferTopic = "order.offer_made"

// LateWindow is how long after a change a drop-if-late notification can still
// be sent, including retries.
const LateWindow = 15 * time.Minute

// Sender is the part of the Bot API the handler needs.
type Sender interface {
	Send(ctx context.Context, chatID int64, text string, keyboard *telegram.InlineKeyboard) (telegram.Message, error)
}

// ReplyTargets remembers sent notifications, so replying to one in Telegram
// starts a chat about its order.
type ReplyTargets interface {
	SaveReplyTarget(ctx context.Context, chatID, messageID int64, orderID uuid.UUID, at time.Time) error
}

// Links is the part of the linking service the handler needs.
type Links interface {
	Get(ctx context.Context, userID uuid.UUID) (linking.Link, error)
	UnlinkChat(ctx context.Context, chatID int64) error
}

// Orders reads the revision of the latest status change applied to an order's
// participants, which the consumer updates before notifying.
type Orders interface {
	// Revision reports found=false for an order with no stored change.
	Revision(ctx context.Context, orderID uuid.UUID) (revision int64, found bool, err error)
	IsCourier(ctx context.Context, orderID, userID uuid.UUID) (bool, error)
}

// EmailValidity preserves the event's delivery class while email is pending.
// Zero fields mean an always-deliver notification.
type EmailValidity struct {
	ExpiresAt        time.Time
	OrderID          uuid.UUID
	Revision         int64
	UnlessReassigned bool
}

// EmailQueue durably accepts email fallback before Kafka commits the event.
type EmailQueue interface {
	Enqueue(ctx context.Context, eventID, userID uuid.UUID, text string, at time.Time, validity EmailValidity) error
}

// Deliveries records which recipients already received or queued a message,
// so a redelivered event cannot switch a queued email to Telegram.
type Deliveries interface {
	HasBeenAccepted(ctx context.Context, eventID, userID uuid.UUID) (bool, error)
	RecordSent(ctx context.Context, eventID, userID uuid.UUID, at time.Time) error
}

// Handler turns one status change into messages for the participants who
// didn't cause it. Delivery is at least once: a crash between sending and
// recording can repeat one message.
type Handler struct {
	// Sender is nil while Telegram is disabled; every recipient then gets the
	// email fallback.
	Sender     Sender
	Links      Links
	Deliveries Deliveries
	Orders     Orders
	// Since is when notifications started. Earlier events are skipped, so a new
	// environment's backlog isn't sent.
	Since time.Time
	// Optional. Without EmailQueue, users Telegram can't reach get no message.
	EmailQueue   EmailQueue
	ReplyTargets ReplyTargets
	Log          *slog.Logger
	Now          func() time.Time
}

// Handle implements a pkg/kafka handler for Topic. It returns an error only for
// failures worth replaying: storage errors and transient Telegram errors.
// Email delivery retries run independently after durable acceptance.
func (h *Handler) Handle(ctx context.Context, _ kafka.Metadata, e *eventsv1.OrderStatusChanged) error {
	log := h.Log.With("event_id", e.GetEventId(), "order_id", e.GetOrderId(), "status", e.GetStatus().String())
	eventID, err := uuid.Parse(e.GetEventId())
	if err != nil {
		// Replaying can't fix a malformed event, so skip it.
		log.Error("skipping order event without a valid event ID")
		return nil
	}
	occurred := e.GetOccurredAt().AsTime()
	if occurred.Before(h.Since) {
		return nil
	}

	for _, r := range recipients(e) {
		text := message(e, r.role)
		if text == "" {
			continue
		}
		class := deliveryFor(e, r.role)
		switch class {
		case dropIfLate:
			if age := h.Now().Sub(occurred); age > LateWindow {
				log.Info("skipping late notification", "role", r.role, "age", age.Round(time.Second).String())
				continue
			}
		case ifStillCurrent:
			current, err := h.isCurrent(ctx, e)
			if err != nil {
				return err
			}
			if !current {
				log.Info("skipping superseded notification", "role", r.role)
				continue
			}
		case unlessReassigned:
			orderID, orderErr := uuid.Parse(e.GetOrderId())
			userID, userErr := uuid.Parse(r.userID)
			if orderErr != nil || userErr != nil {
				continue
			}
			assigned, err := h.Orders.IsCourier(ctx, orderID, userID)
			if err != nil {
				return fmt.Errorf("check removed courier assignment: %w", err)
			}
			if assigned {
				continue
			}
		case alwaysDeliver:
		}
		userID, err := uuid.Parse(r.userID)
		if err != nil {
			log.Error("skipping recipient with an invalid user ID", "role", r.role)
			continue
		}
		var validity EmailValidity
		if class == ifStillCurrent {
			validity.OrderID, _ = uuid.Parse(e.GetOrderId())
			validity.Revision = e.GetRevision()
		}
		if class == unlessReassigned {
			validity.OrderID, _ = uuid.Parse(e.GetOrderId())
			validity.UnlessReassigned = true
		}
		if err := h.notify(ctx, log, eventID, e.GetOrderId(), userID, text, emailed(e, r.role), validity); err != nil {
			return err
		}
	}
	return nil
}

// HandleOffer implements a pkg/kafka handler for OfferTopic. It tells the
// requester about a courier's offer, by Telegram or email, within LateWindow:
// an offer can be replaced, withdrawn or overtaken soon after.
func (h *Handler) HandleOffer(ctx context.Context, _ kafka.Metadata, e *eventsv1.OrderOfferMade) error {
	log := h.Log.With("event_id", e.GetEventId(), "order_id", e.GetOrderId(), "offer_id", e.GetOfferId())
	eventID, err1 := uuid.Parse(e.GetEventId())
	requesterID, err2 := uuid.Parse(e.GetRequesterId())
	if err1 != nil || err2 != nil {
		log.Error("skipping offer event with invalid IDs")
		return nil
	}
	occurred := e.GetOccurredAt().AsTime()
	if occurred.Before(h.Since) {
		return nil
	}
	if age := h.Now().Sub(occurred); age > LateWindow {
		log.Info("skipping late offer notification", "age", age.Round(time.Second).String())
		return nil
	}
	return h.notify(
		ctx,
		log,
		eventID,
		e.GetOrderId(),
		requesterID,
		offerMessage(e),
		true,
		EmailValidity{ExpiresAt: occurred.Add(LateWindow)},
	)
}

// isCurrent reports whether no later status change has been applied to the
// order. The participants have their own consumer, so an order can be missing
// because they haven't reached it yet, or because it ended and was deleted.
// A missing order counts as current only within LateWindow: a fresh change
// can't have been superseded yet, while an old one most likely belongs to a
// deleted order.
func (h *Handler) isCurrent(ctx context.Context, e *eventsv1.OrderStatusChanged) (bool, error) {
	orderID, err := uuid.Parse(e.GetOrderId())
	if err != nil {
		return false, nil
	}
	revision, found, err := h.Orders.Revision(ctx, orderID)
	if err != nil {
		return false, fmt.Errorf("get order revision: %w", err)
	}
	if !found {
		return h.Now().Sub(e.GetOccurredAt().AsTime()) <= LateWindow, nil
	}
	return revision <= e.GetRevision(), nil
}

func (h *Handler) notify(
	ctx context.Context,
	log *slog.Logger,
	eventID uuid.UUID,
	orderID string,
	userID uuid.UUID,
	text string,
	byEmail bool,
	validity EmailValidity,
) error {
	accepted, err := h.Deliveries.HasBeenAccepted(ctx, eventID, userID)
	if err != nil {
		return fmt.Errorf("check accepted notification: %w", err)
	}
	if accepted {
		return nil
	}

	if h.Sender == nil {
		return h.notifyByEmail(ctx, eventID, userID, text, byEmail, validity)
	}
	link, err := h.Links.Get(ctx, userID)
	if errors.Is(err, linking.ErrNotLinked) {
		return h.notifyByEmail(ctx, eventID, userID, text, byEmail, validity)
	}
	if err != nil {
		return fmt.Errorf("get Telegram link: %w", err)
	}

	sentMsg, err := h.Sender.Send(ctx, link.ChatID, text, nil)
	var apiErr *telegram.APIError
	switch {
	case err == nil:
	case errors.As(err, &apiErr) && apiErr.ChatGone():
		if err := h.Links.UnlinkChat(ctx, link.ChatID); err != nil {
			return fmt.Errorf("unlink unreachable chat: %w", err)
		}
		log.Info("telegram unlinked: chat unreachable", "user_id", userID, "code", apiErr.Code)
		return h.notifyByEmail(ctx, eventID, userID, text, byEmail, validity)
	case errors.As(err, &apiErr) && apiErr.Rejected():
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
	// The message is already recorded as sent, so a failure here only makes it
	// unrepliable; replaying wouldn't resend it.
	if id, err := uuid.Parse(orderID); h.ReplyTargets != nil && err == nil {
		if err := h.ReplyTargets.SaveReplyTarget(ctx, link.ChatID, sentMsg.MessageID, id, h.Now()); err != nil {
			log.Error("notification can't be replied to", "user_id", userID, "err", err)
		}
	}
	return nil
}

// notifyByEmail durably accepts eligible fallback with its delivery validity.
// Progress updates that the app already shows remain Telegram-only.
func (h *Handler) notifyByEmail(
	ctx context.Context,
	eventID, userID uuid.UUID,
	text string,
	byEmail bool,
	validity EmailValidity,
) error {
	if h.EmailQueue == nil || !byEmail {
		return nil
	}
	if err := h.EmailQueue.Enqueue(ctx, eventID, userID, text, h.Now(), validity); err != nil {
		return fmt.Errorf("queue notification email: %w", err)
	}
	return nil
}
