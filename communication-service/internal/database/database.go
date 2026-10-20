// Package database opens the Postgres pool and applies the embedded goose
// migrations.
package database

import (
	"context"
	"fmt"

	"github.com/AY2627S1-CS3219-P1/FoC/communication-service/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// Open connects to url and checks the connection.
func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate applies every pending migration.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}

// MigrateURL is the one-shot deployment entrypoint and needs no bot or auth configuration.
func MigrateURL(ctx context.Context, url string) error {
	pool, err := Open(ctx, url)
	if err != nil {
		return err
	}
	defer pool.Close()
	return Migrate(ctx, pool)
}
