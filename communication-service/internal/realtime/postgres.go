package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	db "github.com/AY2627S1-CS3219-P1/FoC/communication-service/internal/database/communicationdb"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Fanout struct {
	Pool *pgxpool.Pool
	Hub  *Hub
}

func (f *Fanout) Publish(ctx context.Context, n Notice) error {
	payload, err := json.Marshal(n)
	if err != nil {
		return err
	}
	return db.New(f.Pool).NotifyChanges(ctx, string(payload))
}

func (f *Fanout) listen(ctx context.Context) error {
	conn, err := f.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// A connection with LISTEN must never return to the general query pool.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = conn.Conn().Close(cleanup)
		conn.Release()
	}()
	if _, err := conn.Exec(ctx, "SET application_name='foc-realtime-listener'; LISTEN foc_realtime"); err != nil {
		return err
	}
	f.Hub.SetReady(true)
	defer f.Hub.SetReady(false)
	slog.InfoContext(ctx, "Live update listener connected")
	for ctx.Err() == nil {
		wait, cancel := context.WithTimeout(ctx, 30*time.Second)
		notification, err := conn.Conn().WaitForNotification(wait)
		cancel()
		if errors.Is(err, context.DeadlineExceeded) {
			ping, stop := context.WithTimeout(ctx, 5*time.Second)
			err := conn.Ping(ping)
			stop()
			if err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		var n Notice
		if err := json.Unmarshal([]byte(notification.Payload), &n); err != nil {
			slog.WarnContext(ctx, "Invalid live update ignored", "error", err)
			continue
		}
		f.Hub.Notify(n)
	}
	return ctx.Err()
}

func (f *Fanout) Run(ctx context.Context) {
	defer f.Hub.SetReady(false)
	delay := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		if err := f.listen(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "Live update listener reconnecting", "error", err)
		}
		if time.Since(started) > time.Minute {
			delay = time.Second
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		delay = min(delay*2, 30*time.Second)
	}
}
