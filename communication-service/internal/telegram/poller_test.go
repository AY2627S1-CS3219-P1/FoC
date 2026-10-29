package telegram

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSource returns scripted results, then blocks until the context ends.
type fakeSource struct {
	mu       sync.Mutex
	results  []func() ([]Update, error)
	offsets  []int64
	timeouts []time.Duration
}

func (f *fakeSource) GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error) {
	f.mu.Lock()
	f.offsets = append(f.offsets, offset)
	f.timeouts = append(f.timeouts, timeout)
	if len(f.results) == 0 {
		f.mu.Unlock()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	next := f.results[0]
	f.results = f.results[1:]
	f.mu.Unlock()
	return next()
}

func (f *fakeSource) seenOffsets() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int64(nil), f.offsets...)
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestPollerStopsOnRejectedToken(t *testing.T) {
	source := &fakeSource{results: []func() ([]Update, error){
		func() ([]Update, error) { return nil, ErrUnauthorized },
	}}
	poller := &Poller{Source: source, Log: quietLog(), Handle: func(context.Context, Update) error { return nil }}

	if err := poller.Run(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Run = %v, want ErrUnauthorized", err)
	}
}

func TestPollerRetriesContinuationBeforeLaterUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	source := &fakeSource{results: []func() ([]Update, error){
		func() ([]Update, error) { return []Update{{UpdateID: 10}, {UpdateID: 11}}, nil },
		func() ([]Update, error) { cancel(); return nil, nil },
	}}
	var steps []string
	attempts := 0
	var resume func(context.Context) error
	resume = func(context.Context) error {
		attempts++
		steps = append(steps, "resume")
		if attempts == 1 {
			return &RetryError{Err: errors.New("still unavailable"), Retry: resume}
		}
		return nil
	}
	poller := &Poller{
		Source:  source,
		Log:     quietLog(),
		Backoff: time.Millisecond,
		Handle: func(_ context.Context, u Update) error {
			if u.UpdateID == 10 {
				steps = append(steps, "first")
				return &RetryError{Err: errors.New("unavailable"), Retry: resume}
			}
			steps = append(steps, "second")
			return nil
		},
	}
	if err := poller.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps, ","); got != "first,resume,resume,second" {
		t.Fatalf("steps = %s", got)
	}
	if offsets := source.seenOffsets(); len(offsets) != 2 || offsets[1] != 12 {
		t.Fatalf("offsets = %v", offsets)
	}
}

func TestPollerCancellationStopsPendingRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &fakeSource{results: []func() ([]Update, error){
		func() ([]Update, error) { return []Update{{UpdateID: 10}, {UpdateID: 11}}, nil },
	}}
	handled := 0
	poller := &Poller{Source: source, Log: quietLog(), Handle: func(context.Context, Update) error {
		handled++
		cancel()
		return &RetryError{Err: errors.New("unavailable"), Retry: func(context.Context) error {
			t.Fatal("retried after cancellation")
			return nil
		}}
	}}
	if err := poller.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if handled != 1 || len(source.seenOffsets()) != 1 {
		t.Fatalf("handled=%d offsets=%v", handled, source.seenOffsets())
	}
}

func TestPollerStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	poller := &Poller{
		Source: &fakeSource{},
		Log:    quietLog(),
		Handle: func(context.Context, Update) error { return nil },
	}
	go func() { done <- poller.Run(ctx) }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestPollerDefaultsToLongPolling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &fakeSource{results: []func() ([]Update, error){
		func() ([]Update, error) { cancel(); return nil, nil },
	}}
	poller := &Poller{Source: source, Log: quietLog(), Handle: func(context.Context, Update) error { return nil }}

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	if len(source.timeouts) == 0 || source.timeouts[0] != DefaultPollTimeout {
		t.Fatalf("timeouts = %v, want %v", source.timeouts, DefaultPollTimeout)
	}
}

func TestPollerCancelsDuringFailedUpdateBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := &fakeSource{results: []func() ([]Update, error){
		func() ([]Update, error) { return []Update{{UpdateID: 10}, {UpdateID: 11}}, nil },
	}}
	var handled []int64
	poller := &Poller{
		Source:  source,
		Log:     quietLog(),
		Backoff: time.Hour,
		Handle: func(_ context.Context, update Update) error {
			handled = append(handled, update.UpdateID)
			cancel()
			return &RetryError{
				Err: errors.New("database unavailable"),
				Retry: func(context.Context) error {
					t.Error("retry continuation ran after cancellation")
					return nil
				},
			}
		},
	}
	if err := poller.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(handled, []int64{10}) || !reflect.DeepEqual(source.seenOffsets(), []int64{0}) {
		t.Fatalf("handled=%v offsets=%v", handled, source.seenOffsets())
	}
}

func TestPollerStopsOnRejectedTokenFromHandler(t *testing.T) {
	source := &fakeSource{results: []func() ([]Update, error){
		func() ([]Update, error) { return []Update{{UpdateID: 10}}, nil },
	}}
	poller := &Poller{
		Source: source,
		Log:    quietLog(),
		Handle: func(context.Context, Update) error { return ErrUnauthorized },
	}
	if err := poller.Run(t.Context()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if !reflect.DeepEqual(source.seenOffsets(), []int64{0}) {
		t.Fatalf("offsets=%v", source.seenOffsets())
	}
}
