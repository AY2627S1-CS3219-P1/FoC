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

// Handler links chats through /start and unlinks chats that block the bot.
type Handler struct {
	Sender Sender
	Linker Linker
	Log    *slog.Logger
}

const (
	startReply   = "Hi! This is the Friend on Campus bot. To get order updates here, choose Connect Telegram in the FoC app."
	linkedReply  = "Connected. You'll get FoC order updates here. You can disconnect anytime from your FoC profile."
	invalidReply = "This link has expired or was already used. Choose Connect Telegram in the FoC app to get a new one."
)

// Handle processes one update. Only private chats are handled.
func (h *Handler) Handle(ctx context.Context, update telegram.Update) error {
	switch {
	case update.MyChatMember != nil:
		return h.handleMembership(ctx, update.MyChatMember)
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
	if command != "/start" {
		return nil
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
		return fmt.Errorf("redeem link code: %w", err)
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
