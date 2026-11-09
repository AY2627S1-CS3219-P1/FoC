// Package database embeds and applies Credit's owned schema migrations.
package database

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed schema/*.sql
var migrations embed.FS

// Migrate applies pending migrations. Externally managed deployments disable
// this startup step with RUN_MIGRATIONS=false.
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
