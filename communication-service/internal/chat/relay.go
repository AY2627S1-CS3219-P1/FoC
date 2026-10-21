package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/google/uuid"
)

// ErrNotFound means the order, reply target or selection isn't stored.
var ErrNotFound = errors.New("not found")

// Telegram's limit on message length, in characters.
const maxMessageLength = 4096

// Callback data prefix for the /chat order buttons.
const selectPrefix = "chat:"

// Sender is the part of the Bot API the relay needs.
type Sender interface {
	Send(ctx context.Context, chatID int64, text string, keyboard *telegram.InlineKeyboard) (telegram.Message, error)
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

// Links is the part of the linking service the relay needs.
type Links interface {
	Get(ctx context.Context, userID uuid.UUID) (linking.Link, error)
	GetByChat(ctx context.Context, chatID int64) (linking.Link, error)
	UnlinkChat(ctx context.Context, chatID int64) error
}

// Store holds participants, routing state and relayed messages.
type Store interface {
	// GetOrder returns ErrNotFound for an unknown order.
	GetOrder(ctx context.Context, id uuid.UUID) (Order, error)
	// UserOrders returns the user's orders that may still be open, newest
	// first. Callers check IsOpen.
	UserOrders(ctx context.Context, userID uuid.UUID, since time.Time) ([]Order, error)
	// ReplyTarget returns ErrNotFound when the message isn't a bot message
	// tied to an order.
	ReplyTarget(ctx context.Context, chatID, messageID int64) (uuid.UUID, error)
	SaveReplyTarget(ctx context.Context, chatID, messageID int64, orderID uuid.UUID, at time.Time) error
	// Selection returns ErrNotFound when the user hasn't chosen an order.
	Selection(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	Select(ctx context.Context, userID, orderID uuid.UUID, at time.Time) error
	// SaveMessage records an accepted attempt; repeating the same ID is a no-op.
	SaveMessage(ctx context.Context, m Message) error
}

// Message records an accepted relay attempt, including attempts that Telegram
// could not deliver. SentAt is when the relay accepted it, before delivery.
type Message struct {
	ID, OrderID, SenderID, RecipientID uuid.UUID
	Body                               string
	SentAt                             time.Time
}

// Relay forwards a participant's Telegram messages to the other participant.
// Senders and recipients see each other's role, never their Telegram account.
type Relay struct {
	Sender Sender
	Links  Links
	Store  Store
	Log    *slog.Logger
	Now    func() time.Time
}

const (
	notLinkedReply    = "Connect Telegram in the FoC app first. Then you can chat about your orders here."
	noChatsReply      = "You have no order chats open. A chat opens when a courier accepts an order."
	chooseReply       = "Which order is this about? Reply to one of its messages, or choose one below, then send your message again."
	textOnlyReply     = "Only text messages can be sent to the other person."
	tooLongReply      = "That message is too long. Please send a shorter one."
	notConnectedReply = "The other person hasn't connected Telegram, so they won't see messages here. Please contact them through the FoC app."
	unreachableReply  = "The other person can't receive Telegram messages right now. Please contact them through the FoC app."
	retryReply        = "That message couldn't be delivered just now. Please send it again."
	closedReply       = "Chat for that order has closed."
)

// HandleMessage relays a message that isn't a command. It replies to the
// sender with what went wrong when it can't relay. Retryable errors retain only
// the unfinished storage or feedback step.
func (r *Relay) HandleMessage(ctx context.Context, msg *telegram.Message) error {
	chatID := msg.Chat.ID
	sender, err := r.Links.GetByChat(ctx, chatID)
	if errors.Is(err, linking.ErrNotLinked) {
		return r.reply(ctx, chatID, notLinkedReply, nil)
	}
	if err != nil {
		return fmt.Errorf("get sender link: %w", err)
	}
	if strings.TrimSpace(msg.Text) == "" {
		return r.reply(ctx, chatID, textOnlyReply, nil)
	}

	now := r.Now()
	order, err := r.route(ctx, sender.UserID, msg, now)
	if errors.Is(err, errNoChats) {
		return r.reply(ctx, chatID, noChatsReply, nil)
	}
	if errors.Is(err, errClosed) {
		return r.reply(ctx, chatID, closedReply, nil)
	}
	var choose chooseError
	if errors.As(err, &choose) {
		return r.reply(ctx, chatID, chooseReply, keyboard(choose.orders))
	}
	if err != nil {
		return err
	}

	recipientID, _ := order.Other(sender.UserID)
	text := relayedText(order, sender.UserID, msg.Text)
	if utf8.RuneCountInString(text) > maxMessageLength {
		return r.reply(ctx, chatID, tooLongReply, nil)
	}
	_, err = r.Links.Get(ctx, recipientID)
	if errors.Is(err, linking.ErrNotLinked) {
		return r.reply(ctx, chatID, notConnectedReply, nil)
	}
	if err != nil {
		return fmt.Errorf("get recipient link: %w", err)
	}

	id, err := uuid.NewV7()
	if err != nil {
		return fmt.Errorf("message ID: %w", err)
	}
	m := Message{ID: id, OrderID: order.ID, SenderID: sender.UserID,
		RecipientID: recipientID, Body: msg.Text, SentAt: now}
	return r.recordAndSend(ctx, m, chatID)
}

// Persist the dispute record before any external delivery. Retrying this stage
// keeps the same ID, including when an insert committed before returning an error.
func (r *Relay) recordAndSend(ctx context.Context, m Message, senderChatID int64) error {
	retry := func(err error) error {
		return &telegram.RetryError{Err: err, Retry: func(ctx context.Context) error {
			return r.recordAndSend(ctx, m, senderChatID)
		}}
	}

	// An insert may resume after retention cleanup or a participant change.
	// Revalidate before writing so a stale continuation cannot recreate records.
	order, err := r.openOrder(ctx, m.SenderID, m.OrderID, r.Now())
	if errors.Is(err, errClosed) {
		return r.reply(ctx, senderChatID, closedReply, nil)
	}
	if err != nil {
		return retry(err)
	}
	if recipientID, _ := order.Other(m.SenderID); recipientID != m.RecipientID {
		return r.reply(ctx, senderChatID, closedReply, nil)
	}

	if err := r.Store.SaveMessage(ctx, m); err != nil {
		return retry(fmt.Errorf("save chat message: %w", err))
	}
	return r.selectAndSend(ctx, m, senderChatID)
}

func (r *Relay) selectAndSend(ctx context.Context, m Message, senderChatID int64) error {
	retry := func(err error) error {
		return &telegram.RetryError{Err: err, Retry: func(ctx context.Context) error {
			return r.selectAndSend(ctx, m, senderChatID)
		}}
	}

	// A database outage can leave this continuation pending while order
	// participants or Telegram links change. Recheck before every send attempt.
	order, err := r.openOrder(ctx, m.SenderID, m.OrderID, r.Now())
	if errors.Is(err, errClosed) {
		return r.reply(ctx, senderChatID, closedReply, nil)
	}
	if err != nil {
		return retry(err)
	}
	if recipientID, _ := order.Other(m.SenderID); recipientID != m.RecipientID {
		return r.reply(ctx, senderChatID, closedReply, nil)
	}
	sender, err := r.Links.Get(ctx, m.SenderID)
	if errors.Is(err, linking.ErrNotLinked) || err == nil && sender.ChatID != senderChatID {
		return r.reply(ctx, senderChatID, notLinkedReply, nil)
	}
	if err != nil {
		return retry(fmt.Errorf("refresh sender link: %w", err))
	}
	recipient, err := r.Links.Get(ctx, m.RecipientID)
	if errors.Is(err, linking.ErrNotLinked) {
		return r.reply(ctx, senderChatID, notConnectedReply, nil)
	}
	if err != nil {
		return retry(fmt.Errorf("refresh recipient link: %w", err))
	}
	text := relayedText(order, m.SenderID, m.Body)
	if utf8.RuneCountInString(text) > maxMessageLength {
		return r.reply(ctx, senderChatID, tooLongReply, nil)
	}
	if err := r.Store.Select(ctx, m.SenderID, m.OrderID, r.Now()); err != nil {
		return retry(fmt.Errorf("select order: %w", err))
	}
	recipientChatID := recipient.ChatID

	sent, err := r.Sender.Send(ctx, recipientChatID, text, nil)
	var apiErr *telegram.APIError
	switch {
	case err == nil:
	case errors.As(err, &apiErr) && apiErr.ChatGone():
		if err := r.Links.UnlinkChat(ctx, recipientChatID); err != nil {
			return fmt.Errorf("unlink unreachable chat: %w", err)
		}
		return r.reply(ctx, senderChatID, unreachableReply, nil)
	default:
		r.Log.Warn("chat message not relayed", "order_id", m.OrderID, "err", err)
		return r.reply(ctx, senderChatID, retryReply, nil)
	}

	return r.saveDeliveredTarget(ctx, recipientChatID, sent.MessageID, m.OrderID, m.SentAt)
}

func (r *Relay) saveDeliveredTarget(ctx context.Context, chatID, messageID int64, orderID uuid.UUID, at time.Time) error {
	if err := r.Store.SaveReplyTarget(ctx, chatID, messageID, orderID, at); err != nil {
		// The poller retries only this write, never the successful Telegram send.
		return &telegram.RetryError{Err: fmt.Errorf("save reply target: %w", err), Retry: func(ctx context.Context) error {
			return r.saveDeliveredTarget(ctx, chatID, messageID, orderID, at)
		}}
	}
	return nil
}

// ShowChats answers /chat with the sender's open order chats as buttons.
func (r *Relay) ShowChats(ctx context.Context, msg *telegram.Message) error {
	sender, err := r.Links.GetByChat(ctx, msg.Chat.ID)
	if errors.Is(err, linking.ErrNotLinked) {
		return r.reply(ctx, msg.Chat.ID, notLinkedReply, nil)
	}
	if err != nil {
		return fmt.Errorf("get sender link: %w", err)
	}
	orders, err := r.openOrders(ctx, sender.UserID, r.Now())
	if err != nil {
		return err
	}
	if len(orders) == 0 {
		return r.reply(ctx, msg.Chat.ID, noChatsReply, nil)
	}
	return r.reply(ctx, msg.Chat.ID,
		"Choose an order to chat about. Your messages go to the other person until you choose another. You can also reply to any message about an order.",
		keyboard(orders))
}

// HandleCallback handles a press of a /chat order button.
func (r *Relay) HandleCallback(ctx context.Context, cb *telegram.CallbackQuery) error {
	data, ok := strings.CutPrefix(cb.Data, selectPrefix)
	orderID, err := uuid.Parse(data)
	if !ok || err != nil || cb.Message == nil || cb.Message.Chat.Type != "private" || cb.Message.Chat.ID != cb.From.ID {
		return r.Sender.AnswerCallback(ctx, cb.ID, "")
	}
	sender, err := r.Links.GetByChat(ctx, cb.From.ID)
	if errors.Is(err, linking.ErrNotLinked) {
		return r.Sender.AnswerCallback(ctx, cb.ID, notLinkedReply)
	}
	if err != nil {
		return fmt.Errorf("get sender link: %w", err)
	}

	now := r.Now()
	order, err := r.Store.GetOrder(ctx, orderID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return fmt.Errorf("get order: %w", err)
	}
	if _, participant := order.Other(sender.UserID); err != nil || !participant || !order.IsOpen(now) {
		return r.Sender.AnswerCallback(ctx, cb.ID, closedReply)
	}
	if err := r.Store.Select(ctx, sender.UserID, order.ID, now); err != nil {
		return fmt.Errorf("select order: %w", err)
	}
	if err := r.Sender.AnswerCallback(ctx, cb.ID, ""); err != nil {
		return fmt.Errorf("answer callback: %w", err)
	}
	return r.reply(ctx, cb.Message.Chat.ID,
		fmt.Sprintf("Your messages now go to your %s for the %s.", otherRole(order, sender.UserID), order.Label()), nil)
}

var (
	errNoChats = errors.New("no open chats")
	errClosed  = errors.New("chat closed")
)

type chooseError struct{ orders []Order }

func (chooseError) Error() string { return "order not chosen" }

// route picks the order a message is about: the order of the message it
// replies to, else the sender's chosen order, else their only open chat.
func (r *Relay) route(ctx context.Context, userID uuid.UUID, msg *telegram.Message, now time.Time) (Order, error) {
	if msg.ReplyToMessage != nil {
		orderID, err := r.Store.ReplyTarget(ctx, msg.Chat.ID, msg.ReplyToMessage.MessageID)
		switch {
		case err == nil:
			return r.openOrder(ctx, userID, orderID, now)
		case !errors.Is(err, ErrNotFound):
			return Order{}, fmt.Errorf("get reply target: %w", err)
		}
		// A reply to a message about no order is routed like a plain message.
	}

	if orderID, err := r.Store.Selection(ctx, userID); err == nil {
		if o, err := r.openOrder(ctx, userID, orderID, now); !errors.Is(err, errClosed) {
			return o, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Order{}, fmt.Errorf("get selection: %w", err)
	}

	orders, err := r.openOrders(ctx, userID, now)
	switch {
	case err != nil:
		return Order{}, err
	case len(orders) == 0:
		return Order{}, errNoChats
	case len(orders) > 1:
		return Order{}, chooseError{orders}
	}
	return orders[0], nil
}

// openOrder returns the order if userID may chat about it now, else errClosed.
func (r *Relay) openOrder(ctx context.Context, userID, orderID uuid.UUID, now time.Time) (Order, error) {
	o, err := r.Store.GetOrder(ctx, orderID)
	if errors.Is(err, ErrNotFound) {
		return Order{}, errClosed
	}
	if err != nil {
		return Order{}, fmt.Errorf("get order: %w", err)
	}
	if _, participant := o.Other(userID); !participant || !o.IsOpen(now) {
		return Order{}, errClosed
	}
	return o, nil
}

func (r *Relay) openOrders(ctx context.Context, userID uuid.UUID, now time.Time) ([]Order, error) {
	orders, err := r.Store.UserOrders(ctx, userID, now.Add(-GracePeriod))
	if err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	open := orders[:0]
	for _, o := range orders {
		if _, participant := o.Other(userID); participant && o.IsOpen(now) {
			open = append(open, o)
		}
	}
	return open, nil
}

func (r *Relay) reply(ctx context.Context, chatID int64, text string, kb *telegram.InlineKeyboard) error {
	if _, err := r.Sender.Send(ctx, chatID, text, kb); err != nil {
		var apiErr *telegram.APIError
		if !errors.Is(err, telegram.ErrUnauthorized) && !(errors.As(err, &apiErr) && apiErr.Rejected()) {
			// Retry only this feedback. Relaying the original update again could
			// deliver a message the sender has already been asked to resend.
			return &telegram.RetryError{Err: fmt.Errorf("send chat feedback: %w", err), Retry: func(ctx context.Context) error {
				return r.reply(ctx, chatID, text, kb)
			}}
		}
		r.Log.Warn("chat reply failed", "chat_id", chatID, "err", err)
	}
	return nil
}

// relayedText labels a message with the sender's role and the order.
func relayedText(o Order, senderID uuid.UUID, body string) string {
	role := "requester"
	if senderID == o.CourierID {
		role = "courier"
	}
	return fmt.Sprintf("Your %s (%s):\n%s", role, o.Label(), body)
}

// otherRole names the participant who isn't userID.
func otherRole(o Order, userID uuid.UUID) string {
	if userID == o.CourierID {
		return "requester"
	}
	return "courier"
}

func keyboard(orders []Order) *telegram.InlineKeyboard {
	kb := &telegram.InlineKeyboard{}
	for _, o := range orders {
		status := strings.ReplaceAll(o.Status, "_", " ")
		label := fmt.Sprintf("%s · %s · %s", o.Label(), status, o.ID.String()[:6])
		kb.Rows = append(kb.Rows, []telegram.InlineButton{{Text: label, CallbackData: selectPrefix + o.ID.String()}})
	}
	return kb
}
