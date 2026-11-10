// Package database embeds and applies Moderation Service's schema migrations.
package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

//go:embed schema/*.sql
var migrations embed.FS

// Migrate applies pending migrations on db. Externally managed deployments
// disable this startup step with RUN_MIGRATIONS=false.
func Migrate(ctx context.Context, db *sql.DB) error {
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
