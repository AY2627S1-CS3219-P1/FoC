package telegramlink

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/linking"
	"github.com/AY2627S1-CS3219-P1/FoC/pkg/auth"
	communicationv1 "github.com/AY2627S1-CS3219-P1/FoC/pkg/gen/communication/v1"
	"github.com/google/uuid"
)

var (
	userID  = uuid.MustParse("4f6c1a2e-0d8b-4b8e-9b77-3f2a1c5d6e7f")
	expires = time.Date(2026, 10, 14, 10, 10, 0, 0, time.UTC)
)

type fakeLogic struct {
	link     linking.Link
	err      error
	unlinked uuid.UUID
}

func (f *fakeLogic) CreateCode(_ context.Context, id uuid.UUID) (string, time.Time, error) {
	if id != userID {
		return "", time.Time{}, errors.New("wrong user")
	}
	return "abc_DEF-123", expires, f.err
}

func (f *fakeLogic) Get(context.Context, uuid.UUID) (linking.Link, error) { return f.link, f.err }

func (f *fakeLogic) Unlink(_ context.Context, id uuid.UUID) error {
	f.unlinked = id
	return f.err
}

func signedIn() context.Context {
	return auth.WithCaller(context.Background(), auth.Caller{ID: userID.String()})
}

func handler(logic *fakeLogic, bot string) *Handler {
	return &Handler{Logic: logic, BotUsername: func() string { return bot }}
}

func TestCreateTelegramLink(t *testing.T) {
	resp, err := handler(&fakeLogic{}, "foc_bot").CreateTelegramLink(signedIn(),
		connect.NewRequest(&communicationv1.CreateTelegramLinkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	link, err := url.Parse(resp.Msg.GetLinkUrl())
	if err != nil || link.String() != "https://t.me/foc_bot?start=abc_DEF-123" {
		t.Fatalf("link = %q", resp.Msg.GetLinkUrl())
	}
	if !resp.Msg.GetExpiresAt().AsTime().Equal(expires) {
		t.Fatalf("expires = %v", resp.Msg.GetExpiresAt().AsTime())
	}
}

func TestCreateTelegramLinkWhileBotDisabled(t *testing.T) {
	_, err := handler(&fakeLogic{}, "").CreateTelegramLink(signedIn(),
		connect.NewRequest(&communicationv1.CreateTelegramLinkRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err = %v, want unavailable", err)
	}
}

func TestRequiresCaller(t *testing.T) {
	h := handler(&fakeLogic{}, "foc_bot")
	ctx := auth.WithCaller(context.Background(), auth.Caller{ID: "not-a-uuid"})
	if _, err := h.GetTelegramLink(ctx, connect.NewRequest(&communicationv1.GetTelegramLinkRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("bad caller ID: err = %v", err)
	}
	if _, err := h.UnlinkTelegram(context.Background(), connect.NewRequest(&communicationv1.UnlinkTelegramRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("no caller: err = %v", err)
	}
}

func TestGetTelegramLink(t *testing.T) {
	linkedAt := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	resp, err := handler(&fakeLogic{link: linking.Link{Username: "kevin", LinkedAt: linkedAt}}, "").
		GetTelegramLink(signedIn(), connect.NewRequest(&communicationv1.GetTelegramLinkRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Msg.GetLinked() || resp.Msg.GetTelegramUsername() != "kevin" || !resp.Msg.GetLinkedAt().AsTime().Equal(linkedAt) {
		t.Fatalf("linked response = %+v", resp.Msg)
	}

	resp, err = handler(&fakeLogic{err: linking.ErrNotLinked}, "").
		GetTelegramLink(signedIn(), connect.NewRequest(&communicationv1.GetTelegramLinkRequest{}))
	if err != nil || resp.Msg.GetLinked() || resp.Msg.TelegramUsername != nil {
		t.Fatalf("unlinked response = %+v, %v", resp, err)
	}
}

func TestUnlinkTelegram(t *testing.T) {
	logic := &fakeLogic{}
	if _, err := handler(logic, "").UnlinkTelegram(signedIn(),
		connect.NewRequest(&communicationv1.UnlinkTelegramRequest{})); err != nil {
		t.Fatal(err)
	}
	if logic.unlinked != userID {
		t.Fatalf("unlinked %v", logic.unlinked)
	}
}

func TestStorageErrorsStayInternal(t *testing.T) {
	_, err := handler(&fakeLogic{err: errors.New("connection to 10.0.0.5 refused")}, "foc_bot").
		GetTelegramLink(signedIn(), connect.NewRequest(&communicationv1.GetTelegramLinkRequest{}))
	if connect.CodeOf(err) != connect.CodeInternal || strings.Contains(err.Error(), "10.0.0.5") {
		t.Fatalf("err = %v", err)
	}
}
