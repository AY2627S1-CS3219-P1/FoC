package chat

import (
	"context"
	"log/slog"
	"time"
)

// Expirer deletes chat data past retention.
type Expirer interface {
	DeleteExpired(ctx context.Context, cutoff, closedBefore time.Time) (int64, error)
}

// CleanupInterval is how often expired chat data is deleted.
const CleanupInterval = time.Hour

// RunCleanup deletes chat messages Retention after their order ends, and
// selections of closed chats, every CleanupInterval until ctx is cancelled.
func RunCleanup(ctx context.Context, store Expirer, log *slog.Logger, now func() time.Time) {
	ticker := time.NewTicker(CleanupInterval)
	defer ticker.Stop()
	for {
		t := now()
		deleted, err := store.DeleteExpired(ctx, t.Add(-Retention), t.Add(-GracePeriod))
		switch {
		case err != nil && ctx.Err() == nil:
			log.Error("chat cleanup failed", "err", err)
		case deleted > 0:
			log.Info("expired chat messages deleted", "count", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
