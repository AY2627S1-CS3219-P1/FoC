package infra

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// WaitRedis pings redis at addr until it responds or ctx/timeout elapses.
func WaitRedis(ctx context.Context, addr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := redis.NewClient(&redis.Options{Addr: addr, DialTimeout: 2 * time.Second})

	for {
		if err := client.Ping(ctx).Err(); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("wait redis timeout: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
			continue
		}
	}
}

// WaitPG connects a throwaway pool to dsn until it responds or ctx/timeout elapses.
func WaitPG(ctx context.Context, dsn string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("wait pg timeout: %w", ctx.Err())
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}

		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			select {
			case <-ctx.Done():
				pool.Close()
				return fmt.Errorf("wait pg timeout: %w", ctx.Err())
			case <-time.After(200 * time.Millisecond):
				continue
			}
		}

		pool.Close()
		return nil
	}
}
