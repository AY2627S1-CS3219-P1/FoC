// Package bot reacts to messages users send to the FoC Telegram bot.
package bot

import (
	"context"
	"log/slog"
	"strings"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
)

// Sender is the part of the Bot API the handler needs.
type Sender interface {
	SendMessage(ctx context.Context, chatID int64, text string) error
}

// Handler answers bot commands. Account linking, notifications and chat relay
// will be added here.
type Handler struct {
	Sender Sender
	Log    *slog.Logger
}

const startReply = "Hi! This is the Friend on Campus bot. Linking your FoC account is coming soon."

// Handle processes one update. Only private-chat messages are answered.
func (h *Handler) Handle(ctx context.Context, update telegram.Update) error {
	msg := update.Message
	if msg == nil || msg.Chat.Type != "private" {
		return nil
	}
	command, _, _ := strings.Cut(strings.TrimSpace(msg.Text), " ")
	switch command {
	case "/start":
		h.Log.Info("telegram start", "chat_id", msg.Chat.ID)
		return h.Sender.SendMessage(ctx, msg.Chat.ID, startReply)
	default:
		return nil
	}
}
