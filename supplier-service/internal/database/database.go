// Package database is responsible for all database interactions.
// This contains function to connect to the database, models and queries.
package database

import (
	"context"
	"log/slog"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yihao03/reminding/internal/database/sqlc"
)

func Connect() (*sqlc.Queries, *pgxpool.Pool) {
	connStr := os.Getenv("DATABASE_URL")
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		slog.Error("Failed to connect to the database", "error", err)
		panic(err)
	}

	return sqlc.New(pool), pool
}
