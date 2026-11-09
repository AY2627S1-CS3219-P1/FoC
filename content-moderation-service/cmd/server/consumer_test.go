package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestConsumeRestartsAfterSubscriberError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var attempts atomic.Int32
	restarted := make(chan struct{})
	done := make(chan struct{})

	go func() {
		consumeWithRetry(ctx, logger, func() error {
			if attempts.Add(1) == 1 {
				return errors.New("unknown subscriber error")
			}
			close(restarted)
			<-ctx.Done()
			return ctx.Err()
		})
		close(done)
	}()

	select {
	case <-restarted:
	case <-time.After(3 * minConsumeBackoff):
		t.Fatal("subscription did not restart after an error")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer did not stop after cancellation")
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("subscription attempts = %d, want 2", got)
	}
}

func TestConsumeCancellationInterruptsRestartBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	var attempts atomic.Int32
	failed := make(chan struct{})
	done := make(chan struct{})

	go func() {
		consumeWithRetry(ctx, logger, func() error {
			attempts.Add(1)
			close(failed)
			return errors.New("subscriber exited")
		})
		close(done)
	}()

	select {
	case <-failed:
	case <-time.After(time.Second):
		t.Fatal("subscription did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("consumer remained in restart backoff after cancellation")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("subscription attempts = %d, want 1", got)
	}
}
