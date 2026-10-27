package telegram

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// UpdateSource is the part of the Bot API the poller needs.
type UpdateSource interface {
	GetUpdates(ctx context.Context, offset int64, timeout time.Duration) ([]Update, error)
}

// Handler processes one update. RetryError delays acknowledgement until its
// continuation succeeds; other errors are logged and acknowledged.
type Handler func(ctx context.Context, update Update) error

// RetryError retains the unfinished part of an update without repeating its
// completed side effects. Retry must return another RetryError if it still fails.
type RetryError struct {
	Err   error
	Retry func(context.Context) error
}

func (e *RetryError) Error() string { return e.Err.Error() }

func (e *RetryError) Unwrap() error { return e.Err }

// DefaultPollTimeout is how long each getUpdates call waits for updates.
const DefaultPollTimeout = 30 * time.Second

// Poller long-polls for updates. Telegram allows one poller per bot token, so
// each developer needs their own bot locally.
type Poller struct {
	Source UpdateSource
	Handle Handler
	Log    *slog.Logger
	// Timeout is the long-poll wait. Zero means DefaultPollTimeout.
	Timeout time.Duration
	// Backoff is the wait after a failed poll. Zero means one second.
	Backoff time.Duration
}

// Run polls until ctx is cancelled. It returns ErrUnauthorized immediately if
// the token is rejected, since retrying can't fix that.
func (p *Poller) Run(ctx context.Context) error {
	backoff := p.Backoff
	if backoff == 0 {
		backoff = time.Second
	}
	timeout := p.Timeout
	if timeout == 0 {
		timeout = DefaultPollTimeout
	}
	var offset int64
	for {
		updates, err := p.Source.GetUpdates(ctx, offset, timeout)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrUnauthorized) {
			return err
		}
		if err != nil {
			p.Log.Warn("telegram poll failed", "err", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			continue
		}
		for _, update := range updates {
			err := p.Handle(ctx, update)
			for err != nil {
				p.Log.Error("telegram update failed", "update_id", update.UpdateID, "err", err)
				var retry *RetryError
				if !errors.As(err, &retry) {
					break
				}
				delay := backoff
				var apiErr *APIError
				if errors.As(err, &apiErr) && apiErr.RetryAfter > delay {
					delay = apiErr.RetryAfter
				}
				select {
				case <-ctx.Done():
					return nil
				case <-time.After(delay):
				}
				err = retry.Retry(ctx)
			}
			offset = update.UpdateID + 1
		}
	}
}
