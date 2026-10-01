package deps

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// Env holds shared handler deps
// Per-request values should stay in context
type Env struct {
	Pool *pgxpool.Pool
}

// New builds the handler environment.
func New(pool *pgxpool.Pool) *Env {
	return &Env{Pool: pool}
}
