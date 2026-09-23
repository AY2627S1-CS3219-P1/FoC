package api

import (
	firebase "firebase.google.com/go/v4"
	"github.com/jackc/pgx/v5/pgxpool"
\"github.com/AY2627S1-CS3219-P1/FoC/supplier-service/internal/database/sqlc"
)

// Env holds shared handler deps
// Per-request values should stay in context
type Env struct {
	Queries  *sqlc.Queries
	Firebase *firebase.App
	Pool     *pgxpool.Pool
}

// NewEnv builds the handler environment.
func NewEnv(queries *sqlc.Queries, app *firebase.App, pool *pgxpool.Pool) *Env {
	return &Env{
		Queries:  queries,
		Firebase: app,
		Pool:     pool,
	}
}
