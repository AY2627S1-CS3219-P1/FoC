//go:build integration

// Package postgres starts a throwaway PostgreSQL for user-service's
// integration tests and returns a GORM pool with the embedded migrations
// applied. The container and pool are torn down automatically when the test
// finishes.
package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"gorm.io/gorm"

	"github.com/AY2627S1-CS3219-P1/FoC/user-service/internal/database"
)

// New spins up a disposable PostgreSQL, applies the embedded migrations, and
// returns a ready GORM pool. maxOpen and maxIdle set the underlying pool
// sizes, so tests can reproduce concurrency limits such as a single
// connection. The container is terminated and the pool closed when the test
// completes.
func New(t *testing.T, maxOpen, maxIdle int) *gorm.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:18",
		postgres.WithDatabase("user_test"),
		postgres.WithUsername("user"),
		postgres.WithPassword("user"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL: %v", err)
	}
	testcontainers.CleanupContainer(t, container)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("database URL: %v", err)
	}

	db, err := database.Open(dsn, maxOpen, maxIdle)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
