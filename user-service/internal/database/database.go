package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"user-service/migrations"
)

// Open connects to Postgres via GORM. TranslateError maps driver errors
// (e.g. unique violations) to gorm.ErrDuplicatedKey etc.
func Open(dsn string, maxOpen, maxIdle int) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		TranslateError: true,
		Logger: logger.New(log.New(os.Stdout, "", log.LstdFlags), logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true, // 404s are not errors
			ParameterizedQueries:      true, // keep emails/PII out of logs
		}),
		NowFunc: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	return db, nil
}

// Migrator returns a goose provider over the SQL files embedded from
// migrations/. The goose CLI works on the same files:
//
//	goose -dir migrations postgres "$DATABASE_URL" status|up|down
func Migrator(db *gorm.DB) (*goose.Provider, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	return goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS)
}

// Migrate applies all pending up-migrations. Versioned SQL instead of GORM
// AutoMigrate so schema changes are reviewable.
func Migrate(db *gorm.DB) error {
	p, err := Migrator(db)
	if err != nil {
		return err
	}
	if _, err := p.Up(context.Background()); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
