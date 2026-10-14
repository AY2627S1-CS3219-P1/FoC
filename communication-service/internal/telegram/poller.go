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

// Handler processes one update. Errors are logged and the update is still
// acknowledged, so one bad message can't stall the bot.
type Handler func(ctx context.Context, update Update) error

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
			if err := p.Handle(ctx, update); err != nil {
				p.Log.Error("telegram update failed", "update_id", update.UpdateID, "err", err)
			}
			offset = update.UpdateID + 1
		}
	}
}
