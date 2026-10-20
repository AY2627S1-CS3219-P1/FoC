package bot

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
	"github.com/google/uuid"
)

type fakeSender struct {
	sent []string
	err  error
}

func (f *fakeSender) SendMessage(_ context.Context, _ int64, text string) error {
	f.sent = append(f.sent, text)
	return f.err
}

type fakeLinker struct {
	redeemErr     error
	unlinkErr     error
	redeemedCode  string
	redeemedChat  linking.Chat
	unlinkedChats []int64
}

func (f *fakeLinker) Redeem(_ context.Context, code string, chat linking.Chat) (linking.Link, error) {
	f.redeemedCode, f.redeemedChat = code, chat
	if f.redeemErr != nil {
		return linking.Link{}, f.redeemErr
	}
	return linking.Link{UserID: uuid.New(), ChatID: chat.ID}, nil
}

func (f *fakeLinker) UnlinkChat(_ context.Context, chatID int64) error {
	f.unlinkedChats = append(f.unlinkedChats, chatID)
	return f.unlinkErr
}

func message(chatType, text string) telegram.Update {
	return telegram.Update{Message: &telegram.Message{
		Chat: telegram.Chat{ID: 42, Type: chatType},
		From: &telegram.User{ID: 7, Username: "kevin"},
		Text: text,
	}}
}

func membership(chatType, status string) telegram.Update {
	return telegram.Update{MyChatMember: &telegram.ChatMemberUpdated{
		Chat:          telegram.Chat{ID: 42, Type: chatType},
		NewChatMember: telegram.ChatMember{Status: status},
	}}
}

func newHandler(linker *fakeLinker) (*Handler, *fakeSender) {
	sender := &fakeSender{}
	return &Handler{Sender: sender, Linker: linker, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, sender
}

func TestStartWithoutCodeExplainsLinking(t *testing.T) {
	linker := &fakeLinker{}
	h, sender := newHandler(linker)
	if err := h.Handle(context.Background(), message("private", "/start")); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != startReply || linker.redeemedCode != "" {
		t.Fatalf("sent = %v, redeemed %q", sender.sent, linker.redeemedCode)
	}
}

func TestStartWithCodeLinksChat(t *testing.T) {
	linker := &fakeLinker{}
	h, sender := newHandler(linker)
	if err := h.Handle(context.Background(), message("private", "/start abc_DEF-123")); err != nil {
		t.Fatal(err)
	}
	if linker.redeemedCode != "abc_DEF-123" || linker.redeemedChat != (linking.Chat{ID: 42, Username: "kevin"}) {
		t.Fatalf("redeemed %q for %+v", linker.redeemedCode, linker.redeemedChat)
	}
	if len(sender.sent) != 1 || sender.sent[0] != linkedReply {
		t.Fatalf("sent = %v", sender.sent)
	}
}

func TestStartWithBadCodeExplainsExpiry(t *testing.T) {
	h, sender := newHandler(&fakeLinker{redeemErr: linking.ErrInvalidCode})
	if err := h.Handle(context.Background(), message("private", "/start old")); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != invalidReply {
		t.Fatalf("sent = %v", sender.sent)
	}
}

func TestStartReturnsStorageFailureForPollingRetry(t *testing.T) {
	failure := errors.New("database down")
	h, sender := newHandler(&fakeLinker{redeemErr: failure})
	if err := h.Handle(t.Context(), message("private", "/start code")); !errors.Is(err, failure) {
		t.Fatalf("expected storage error, got %v", err)
	}
	if len(sender.sent) != 0 {
		t.Fatalf("sent = %v", sender.sent)
	}
}

func TestReplyFailureDoesNotReplaySuccessfulRedemption(t *testing.T) {
	h, sender := newHandler(&fakeLinker{})
	sender.err = errors.New("sendMessage failed: 403 bot blocked")
	if err := h.Handle(t.Context(), message("private", "/start code")); err != nil {
		t.Fatalf("successful redemption must be acknowledged despite reply failure: %v", err)
	}
	if len(sender.sent) != 1 || sender.sent[0] != linkedReply {
		t.Fatalf("sent=%v", sender.sent)
	}
}

func TestIgnoredMessages(t *testing.T) {
	for name, update := range map[string]telegram.Update{
		"group start": message("group", "/start code"),
		"other text":  message("private", "hello"),
		"empty":       {UpdateID: 1},
	} {
		linker := &fakeLinker{}
		h, sender := newHandler(linker)
		if err := h.Handle(context.Background(), update); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(sender.sent) != 0 || linker.redeemedCode != "" {
			t.Fatalf("%s: sent %v, redeemed %q", name, sender.sent, linker.redeemedCode)
		}
	}
}

func TestBlockingTheBotUnlinksChat(t *testing.T) {
	linker := &fakeLinker{}
	h, _ := newHandler(linker)
	if err := h.Handle(context.Background(), membership("private", "kicked")); err != nil {
		t.Fatal(err)
	}
	if len(linker.unlinkedChats) != 1 || linker.unlinkedChats[0] != 42 {
		t.Fatalf("unlinked = %v", linker.unlinkedChats)
	}

	for _, update := range []telegram.Update{membership("private", "member"), membership("group", "kicked")} {
		linker := &fakeLinker{}
		h, _ := newHandler(linker)
		if err := h.Handle(context.Background(), update); err != nil {
			t.Fatal(err)
		}
		if len(linker.unlinkedChats) != 0 {
			t.Fatalf("unexpected unlink for %+v", update.MyChatMember)
		}
	}
}

func TestBlockingReturnsStorageFailureForPollingRetry(t *testing.T) {
	failure := errors.New("database unavailable")
	h, _ := newHandler(&fakeLinker{unlinkErr: failure})
	if err := h.Handle(t.Context(), membership("private", "kicked")); !errors.Is(err, failure) {
		t.Fatalf("expected unlink error, got %v", err)
	}
}

func TestReplyStopsPollingWhenTokenRejected(t *testing.T) {
	h, sender := newHandler(&fakeLinker{})
	sender.err = telegram.ErrUnauthorized
	if err := h.Handle(t.Context(), message("private", "/start")); !errors.Is(err, telegram.ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
}
