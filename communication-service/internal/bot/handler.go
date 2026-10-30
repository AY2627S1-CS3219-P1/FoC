// Package bot reacts to what users do with the FoC Telegram bot.
package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
)

// Sender is the part of the Bot API the handler needs.
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Linker is the part of the linking service the handler needs.
type Linker interface {
	Redeem(ctx context.Context, code string, chat linking.Chat) (linking.Link, error)
	UnlinkChat(ctx context.Context, chatID int64) error
}

// Chat is the part of the chat relay the handler needs.
type Chat interface {
	HandleMessage(ctx context.Context, msg *telegram.Message) error
	ShowChats(ctx context.Context, msg *telegram.Message) error
	HandleCallback(ctx context.Context, cb *telegram.CallbackQuery) error
}

// Handler links chats through /start, unlinks chats that block the bot, and
// passes everything else to the chat relay.
type Handler struct {
	Sender Sender
	Linker Linker
	Chat   Chat
	Log    *slog.Logger
}

const (
	startReply   = "Hi! This is the Friend on Campus bot. To get order updates and chat with your courier or requester here, choose Connect Telegram in the FoC app."
	helpReply    = "Send a message to chat with the other person on your order. Reply to a message about an order to answer about that order, or use /chat to choose one."
	linkedReply  = "Connected. You'll get FoC order updates here. You can disconnect anytime from your FoC profile."
	invalidReply = "This link has expired or was already used. Choose Connect Telegram in the FoC app to get a new one."
	retryReply   = "Something went wrong connecting your account. Please press Start again."
)

// Handle processes one update. Only private chats are handled.
func (h *Handler) Handle(ctx context.Context, update telegram.Update) error {
	switch {
	case update.MyChatMember != nil:
		return h.handleMembership(ctx, update.MyChatMember)
	case update.CallbackQuery != nil:
		return h.Chat.HandleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		return h.handleMessage(ctx, update.Message)
	default:
		return nil
	}
}

func (h *Handler) handleMessage(ctx context.Context, msg *telegram.Message) error {
	if msg.Chat.Type != "private" {
		return nil
	}
	command, code, _ := strings.Cut(strings.TrimSpace(msg.Text), " ")
	switch {
	case command == "/chat":
		return h.Chat.ShowChats(ctx, msg)
	case command == "/help":
		return h.Sender.SendMessage(ctx, msg.Chat.ID, helpReply)
	case strings.HasPrefix(command, "/") && command != "/start":
		return h.Sender.SendMessage(ctx, msg.Chat.ID, helpReply)
	case command != "/start":
		return h.Chat.HandleMessage(ctx, msg)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return h.Sender.SendMessage(ctx, msg.Chat.ID, startReply)
	}

	chat := linking.Chat{ID: msg.Chat.ID}
	if msg.From != nil {
		chat.Username = msg.From.Username
	}
	link, err := h.Linker.Redeem(ctx, code, chat)
	if errors.Is(err, linking.ErrInvalidCode) {
		h.Log.Info("telegram link code rejected", "chat_id", msg.Chat.ID)
		return h.Sender.SendMessage(ctx, msg.Chat.ID, invalidReply)
	}
	if err != nil {
		// The poller acknowledges this update anyway, but the code is still
		// unused, so the user can retry.
		h.Log.Error("telegram link failed", "chat_id", msg.Chat.ID, "err", err)
		return h.Sender.SendMessage(ctx, msg.Chat.ID, retryReply)
	}
	h.Log.Info("telegram linked", "user_id", link.UserID, "chat_id", link.ChatID)
	return h.Sender.SendMessage(ctx, msg.Chat.ID, linkedReply)
}

func (h *Handler) handleMembership(ctx context.Context, change *telegram.ChatMemberUpdated) error {
	if change.Chat.Type != "private" || change.NewChatMember.Status != "kicked" {
		return nil
	}
	if err := h.Linker.UnlinkChat(ctx, change.Chat.ID); err != nil {
		return fmt.Errorf("unlink blocked chat: %w", err)
	}
	h.Log.Info("telegram unlinked after block", "chat_id", change.Chat.ID)
	return nil
}
