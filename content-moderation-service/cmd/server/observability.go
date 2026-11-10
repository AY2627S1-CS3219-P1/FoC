package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Runs until shutdown, before the shared database pool closes.
func reportOperationalState(
	ctx context.Context,
	pool *pgxpool.Pool,
	components serverComponents,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		q := moderationdb.New(pool)
		stats, err := q.GetOperationalStats(queryCtx)
		if err != nil {
			if ctx.Err() == nil {
				slog.WarnContext(ctx, "read moderation operational state", "error", err)
			}
		} else {
			slog.InfoContext(ctx, "moderation operational state", "queue_depth", stats.QueueDepth,
				"oldest_job_at", stats.OldestJobAt, "exhausted_jobs", stats.ExhaustedJobs,
				"pending_outbox", stats.PendingOutbox, "oldest_outbox_at", stats.OldestOutboxAt)
			if components.pool != nil {
				components.pool.LogStatistics(ctx)
			}
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
