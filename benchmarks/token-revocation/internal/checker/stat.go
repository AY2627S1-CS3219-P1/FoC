package checker

import (
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PoolStat is a portable snapshot of a Postgres pool's wait behaviour, used to
// report connection-pool waits without leaking pgxpool into the load/render
// packages. In the pinned pgx v5.7.2 the snapshot exposes its figures as
// methods, so PoolStatFrom gathers them here. EmptyAcquireCount is the
// pool-wait indicator: the number of acquires that had to queue because the
// pool was empty.
type PoolStat struct {
	Size              int32 // MaxConns
	Idle              int32 // IdleConns
	TotalConns        int32 // total connections currently in the pool
	AcquiredConns     int32 // connections currently checked out
	AcquireCount      int64 // cumulative successful acquires
	AcquireDurationMs float64
	EmptyAcquireCount int64 // acquires that waited on an empty pool (pool-wait)
}

// PoolStatFrom converts a pgxpool snapshot into the portable form.
func PoolStatFrom(s *pgxpool.Stat) *PoolStat {
	if s == nil {
		return nil
	}
	return &PoolStat{
		Size:              s.MaxConns(),
		Idle:              s.IdleConns(),
		TotalConns:        s.TotalConns(),
		AcquiredConns:     s.AcquiredConns(),
		AcquireCount:      s.AcquireCount(),
		AcquireDurationMs: float64(s.AcquireDuration()) / float64(time.Millisecond),
		EmptyAcquireCount: s.EmptyAcquireCount(),
	}
}

// StatProvider is implemented by checkers that can report connection-pool
// waits. main type-asserts the checker to this to attach pool-wait metrics to
// the Result. Postgres and HTTP implement it; Redis multiplexes over a fixed
// set of connections and reports none.
type StatProvider interface {
	// PoolStats returns a snapshot of the checker's pool waits, or nil when
	// there is no pool to report on.
	PoolStats() *PoolStat
}
