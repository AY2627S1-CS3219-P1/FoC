package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
	if _, err := waitForBot(
		context.Background(),
		bot,
		discard,
		time.Millisecond,
	); !errors.Is(err, telegram.ErrUnauthorized) ||
		bot.calls != 1 {
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

func TestNotificationRetryDelay(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want time.Duration
	}{
		{"network failure", errors.New("connection reset"), time.Second},
		{
			"long rate limit",
			fmt.Errorf("handler: %w", &telegram.APIError{Code: 429, RetryAfter: 2 * time.Minute}),
			2 * time.Minute,
		},
		{"short rate limit", &telegram.APIError{Code: 429, RetryAfter: time.Millisecond}, time.Second},
		{"other API failure", &telegram.APIError{Code: 502}, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notificationRetryDelay(tc.err, time.Second); got != tc.want {
				t.Fatalf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBotShutdownJoinsStartedWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bottest/getMe" {
			_, _ = io.WriteString(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"foc_bot"}}`)
			return
		}
		<-r.Context().Done()
	}))
	defer api.Close()

	var workers sync.WaitGroup
	var username atomic.Pointer[string]
	ready := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	startBot(ctx, config{telegramToken: "test", telegramAPI: api.URL}, discard, nil, nil, &username, func() {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-release
		}()
		close(ready)
	}, &workers)
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("bot did not start")
	}
	cancel()
	joined := make(chan struct{})
	go func() {
		workers.Wait()
		close(joined)
	}()
	select {
	case <-joined:
		t.Fatal("shutdown returned while a started worker was still running")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join workers after cancellation")
	}
	if username.Load() != nil {
		t.Fatal("bot did not finish before workers were joined")
	}
}
