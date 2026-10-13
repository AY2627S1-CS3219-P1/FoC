package bot

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
)

type sentMessage struct {
	chatID int64
	text   string
}

type fakeSender struct{ sent []sentMessage }

func (f *fakeSender) SendMessage(_ context.Context, chatID int64, text string) error {
	f.sent = append(f.sent, sentMessage{chatID, text})
	return nil
}

func update(chatType, text string) telegram.Update {
	return telegram.Update{UpdateID: 1, Message: &telegram.Message{Chat: telegram.Chat{ID: 42, Type: chatType}, Text: text}}
}

func TestHandle(t *testing.T) {
	cases := map[string]struct {
		update    telegram.Update
		wantReply bool
	}{
		"start in private chat":    {update("private", "/start"), true},
		"start with payload":       {update("private", "/start abc123"), true},
		"start in group":           {update("group", "/start"), false},
		"other text":               {update("private", "hello"), false},
		"update without a message": {telegram.Update{UpdateID: 2}, false},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			sender := &fakeSender{}
			h := &Handler{Sender: sender, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
			if err := h.Handle(context.Background(), c.update); err != nil {
				t.Fatalf("Handle: %v", err)
			}
			if got := len(sender.sent) == 1; got != c.wantReply {
				t.Fatalf("sent = %v, want reply %v", sender.sent, c.wantReply)
			}
			if c.wantReply && sender.sent[0].chatID != 42 {
				t.Fatalf("replied to chat %d", sender.sent[0].chatID)
			}
		})
	}
}
