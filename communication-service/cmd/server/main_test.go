package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/telegram"
)

// fakeBot fails GetMe with each error in order, then succeeds.
type fakeBot struct {
	errs  []error
	calls int
}

func (f *fakeBot) GetMe(context.Context) (telegram.User, error) {
	f.calls++
	if f.calls <= len(f.errs) {
		return telegram.User{}, f.errs[f.calls-1]
	}
	return telegram.User{Username: "foc_bot"}, nil
}

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestWaitForBotRetriesTransientFailures(t *testing.T) {
	bot := &fakeBot{errs: []error{errors.New("dial tcp: timeout"), errors.New("getMe failed: 502 Bad Gateway")}}
	me, err := waitForBot(context.Background(), bot, discard, time.Millisecond)
	if err != nil || me.Username != "foc_bot" || bot.calls != 3 {
		t.Fatalf("me=%+v err=%v calls=%d", me, err, bot.calls)
	}
}

func TestWaitForBotStopsOnRejectedToken(t *testing.T) {
	bot := &fakeBot{errs: []error{telegram.ErrUnauthorized}}
	if _, err := waitForBot(context.Background(), bot, discard, time.Millisecond); !errors.Is(err, telegram.ErrUnauthorized) || bot.calls != 1 {
		t.Fatalf("err=%v calls=%d", err, bot.calls)
	}
}

func TestWaitForBotStopsWhenCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	bot := &fakeBot{errs: []error{errors.New("connection reset")}}
	if _, err := waitForBot(ctx, bot, discard, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
