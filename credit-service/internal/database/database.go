// Package database connects to Credit's PostgreSQL database. Queries are
// generated into creditdb by sqlc.
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
