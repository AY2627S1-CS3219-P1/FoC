// Package database is responsible for all database interactions.
// This contains function to connect to the database, models and queries.
package database

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

func Connect(connStr string) *pgxpool.Pool {
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		slog.Error("Failed to connect to the database", "error", err)
		panic(err)
	}

	return pool
}
