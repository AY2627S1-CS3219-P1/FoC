package database

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
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

// Migrate applies all pending up-migrations embedded in the binary.
// Versioned SQL instead of GORM AutoMigrate so schema changes are reviewable.
func Migrate(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	drv, err := pgxmigrate.WithInstance(sqlDB, &pgxmigrate.Config{})
	if err != nil {
		return err
	}
	m, err := migrate.NewWithInstance("iofs", src, "pgx5", drv)
	if err != nil {
		return err
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
