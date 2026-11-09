// Package database connects the content moderation service to PostgreSQL,
// embeds its owned schema migrations, and runs application transactions.
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/content-moderation-service/internal/database/moderationdb"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// ErrSerializationFailure signals a Postgres serialization/deadlock victim. The
// caller may retry the transaction without losing committed work.
var ErrSerializationFailure = errors.New("serialization failure")

//go:embed schema/*.sql
var migrations embed.FS

func Connect(connStr string) *pgxpool.Pool {
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		panic(err)
	}
	return pool
}

// Migrate applies pending owned migrations embedded at build time.
func Migrate(ctx context.Context, databaseURL string) (err error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()

	schema, err := fs.Sub(migrations, "schema")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, schema)
	if err != nil {
		return fmt.Errorf("construct migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// MigrateDownAll reverts every owned migration, leaving the schema empty. It
// exists for test suites that verify up/down/up cycles against a throwaway
// database.
func MigrateDownAll(ctx context.Context, databaseURL string) (err error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open migration database: %w", err)
	}
	defer func() { err = errors.Join(err, db.Close()) }()

	schema, err := fs.Sub(migrations, "schema")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, schema)
	if err != nil {
		return fmt.Errorf("construct migrations: %w", err)
	}
	if _, err := provider.DownTo(ctx, 0); err != nil {
		return fmt.Errorf("revert migrations: %w", err)
	}
	return nil
}

// Within runs f in a READ COMMITTED transaction, mapping serialization and
// deadlock failures to ErrSerializationFailure so callers can retry.
func Within(ctx context.Context, pool *pgxpool.Pool, f func(*moderationdb.Queries) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanupCtx)
	}()

	if err := f(moderationdb.New(tx)); err != nil {
		if isSerialization(err) {
			return fmt.Errorf("transaction: %w", ErrSerializationFailure)
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if isSerialization(err) {
			return fmt.Errorf("commit transaction: %w", ErrSerializationFailure)
		}
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func isSerialization(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001")
}
