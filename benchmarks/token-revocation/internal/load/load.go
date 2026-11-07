// Package load drives the concurrent revocation-check workload and aggregates
// the latency and throughput of one measurement run. The workload can be pure
// reads (the write-free baseline) or a mix of reads and durable writes; reads
// and writes are recorded in separate histograms and counters so that the
// infrequent but slow durable writes never disappear from their own headline
// percentiles behind the dominant read traffic. Each worker keeps its own RNG
// so the hot path is one RNG draw plus one atomic histogram update per op.
package load

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/checker"
)

// LatencyStats is one side (read or write) of a run: histogram percentiles,
// max, throughput, and failure counts.
type LatencyStats struct {
	P50ms      float64 // histogram-estimated median latency (ms)
	P90ms      float64
	P99ms      float64
	Maxms      float64 // upper edge of the highest non-empty latency bucket
	Ops        int64   // successful ops recorded in the measured window
	Throughput float64 // successful ops per second over the measured wall-clock
	Errors     int64   // failed ops (store error, non-2xx)
	Timeouts   int64   // ops cancelled by a deadline
}

// Result aggregates one measurement run against a single checker.
type Result struct {
	Variant     string        // "redis", "pg", "http"
	Pool        int           // Postgres pool size this run used (0 for redis); reported for analysis
	Concurrency int           // number of concurrent workers spawned
	Sessions    int           // size of the seeded present (revoked) population
	Rep         int           // repetition index (0-based) for this variant/pool/level/write-pct
	Duration    time.Duration // measured-window length this run targeted

	WritePct      float64 // configured write fraction (0..1) of logical ops
	AchievedWrite float64 // achieved write fraction across the measured window
	AchievedHit   float64 // achieved read-hit fraction across the measured window

	Read  LatencyStats
	Write LatencyStats

	Ops        int64   // overall successful ops (read + write)
	Throughput float64 // overall ops/s over the measured wall-clock
	Errors     int64   // overall failed ops
	Timeouts   int64   // overall cancelled ops

	ClientCPU float64 // CPU seconds used by this process / wall seconds (≈1 == one full core)

	PoolStats      *checker.PoolStat // connection-pool waits (pg/http); nil where inapplicable
	Durable        bool              // whether this run exercised durable writes
	Durability     map[string]string // effective durability settings this run assumed
	ClientSettings string            // client-level settings (timeouts, statement_timeout) for the run
}

// config is the fully-resolved input to one Run.
type config struct {
	variant        string
	pool           int
	concurrency    int
	sids           []string // seeded present (revoked) keys — the hit population
	absentIDs      []string // keys never inserted — the miss population
	writeIDs       []string // unique write-key pool, disjoint from the read populations
	writePct       float64  // fraction of logical ops that are writes (0..1)
	hitRate        float64  // fraction of reads that hit a seeded present key (0..1)
	entryTTL       time.Duration
	duration       time.Duration
	warmup         time.Duration
	durable        bool
	durability     map[string]string
	clientSettings string
}

// Option configures a Run.
type Option func(*config)

// Option constructors.
func WithVariant(v string) Option         { return func(c *config) { c.variant = v } }
func WithPool(p int) Option               { return func(c *config) { c.pool = p } }
func WithConcurrency(c int) Option        { return func(cfg *config) { cfg.concurrency = c } }
func WithSids(s []string) Option          { return func(cfg *config) { cfg.sids = s } }
func WithAbsent(a []string) Option        { return func(cfg *config) { cfg.absentIDs = a } }
func WithWriteIDs(w []string) Option      { return func(cfg *config) { cfg.writeIDs = w } }
func WithWritePct(p float64) Option       { return func(cfg *config) { cfg.writePct = p } }
func WithHitRate(h float64) Option        { return func(cfg *config) { cfg.hitRate = h } }
func WithEntryTTL(t time.Duration) Option { return func(cfg *config) { cfg.entryTTL = t } }
func WithDuration(d time.Duration) Option { return func(cfg *config) { cfg.duration = d } }
func WithWarmup(w time.Duration) Option   { return func(cfg *config) { cfg.warmup = w } }
func WithDurable(d bool, settings map[string]string) Option {
	return func(cfg *config) { cfg.durable = d; cfg.durability = settings }
}
func WithClientSettings(s string) Option { return func(cfg *config) { cfg.clientSettings = s } }

// windowState holds the per-run counters and histograms shared across workers.
// Reads and writes keep their own, so the two workloads never share a histogram.
type windowState struct {
	readHist    histogram
	writeHist   histogram
	readOps     atomic.Int64
	writeOps    atomic.Int64
	readErrs    atomic.Int64
	writeErrs   atomic.Int64
	readTO      atomic.Int64
	writeTO     atomic.Int64
	hits        atomic.Int64
	writeCursor atomic.Int64 // monotonically increasing index into cfg.writeIDs
}

// Run measures revocation-check latency with the given workload. It warms up
// for cfg.warmup (unmeasured), resets any warm-up writes, then performs the
// mixed read/write workload concurrently for cfg.duration, recording reads and
// writes in separate histograms and counters, and returns the aggregated
// Result. A cancelled ctx stops workers early (counted as timeouts).
//
// cfg.writeIDs, cfg.sids and cfg.absentIDs must be disjoint. Run resets the
// write population at the start of its measured window and, via the caller's
// resetter, between runs.
func Run(ctx context.Context, chk checker.Checker, opts ...Option) Result {
	cfg := &config{duration: 15 * time.Second, warmup: 3 * time.Second}
	for _, o := range opts {
		o(cfg)
	}
	if len(cfg.sids) == 0 && len(cfg.absentIDs) == 0 {
		return Result{Variant: cfg.variant, Pool: cfg.pool, Concurrency: cfg.concurrency}
	}

	return measure(ctx, chk, cfg)
}

// measure runs the warm-up and measured windows and aggregates the Result.
func measure(ctx context.Context, chk checker.Checker, cfg *config) Result {
	state := &windowState{
		readHist:  newHistogram(),
		writeHist: newHistogram(),
	}
	var wg sync.WaitGroup

	// Warm-up: run workers until the warm-up window elapses without recording,
	// so connection pools and caches are hot before the measured window.
	warmStop := time.Now().Add(cfg.warmup)
	for w := 0; w < cfg.concurrency; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			runWorker(ctx, chk, cfg, state, warmStop, false, seed)
		}(int64(w))
	}
	wg.Wait()

	// Reset exactly the writes produced during warm-up so the measured window
	// starts from a clean write-key population (isolation between runs).
	resetWriteIDs(ctx, chk, cfg, state.writeCursor.Load())

	// Measured window.
	startCPU, _ := cpuSeconds()
	measureStart := time.Now()
	stop := measureStart.Add(cfg.duration)
	for w := 0; w < cfg.concurrency; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			runWorker(ctx, chk, cfg, state, stop, true, seed)
		}(int64(w))
	}
	wg.Wait()
	wall := time.Since(measureStart)
	endCPU, _ := cpuSeconds()

	// Reset the writes produced during the measured window before returning, so
	// they do not contaminate the next repetition.
	resetWriteIDs(ctx, chk, cfg, state.writeCursor.Load())

	read := finalizeLatency(state.readHist, state.readOps.Load(), wall, state.readErrs.Load(), state.readTO.Load())
	write := finalizeLatency(state.writeHist, state.writeOps.Load(), wall, state.writeErrs.Load(), state.writeTO.Load())
	totalOps := state.readOps.Load() + state.writeOps.Load()
	totalErrs := state.readErrs.Load() + state.writeErrs.Load()
	totalTO := state.readTO.Load() + state.writeTO.Load()

	r := Result{
		Variant:        cfg.variant,
		Pool:           cfg.pool,
		Concurrency:    cfg.concurrency,
		Sessions:       len(cfg.sids),
		Duration:       cfg.duration,
		WritePct:       cfg.writePct,
		AchievedWrite:  writeFraction(totalOps, state.writeOps.Load()),
		AchievedHit:    hitFraction(state.readOps.Load(), state.hits.Load()),
		Read:           read,
		Write:          write,
		Ops:            totalOps,
		Throughput:     float64(totalOps) / wall.Seconds(),
		Errors:         totalErrs,
		Timeouts:       totalTO,
		ClientCPU:      (endCPU - startCPU) / wall.Seconds(),
		Durable:        cfg.durable,
		Durability:     cfg.durability,
		ClientSettings: cfg.clientSettings,
	}
	return r
}

// runWorker runs one worker loop. When measured it records latency and errors
// into state; otherwise it only drives the workload (warm-up) and advances the
// write cursor so the caller can reset exactly what was written.
func runWorker(ctx context.Context, chk checker.Checker, cfg *config, state *windowState, stop time.Time, measured bool, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	writeIDs := cfg.writeIDs
	sids := cfg.sids
	absent := cfg.absentIDs

	for time.Now().Before(stop) {
		if ctx.Err() != nil {
			return
		}
		if rng.Float64() < cfg.writePct {
			t0 := time.Now()
			id := writeID(writeIDs, state.writeCursor.Add(1)-1)
			err := chk.Revoke(ctx, id, cfg.entryTTL)
			if measured {
				classifyWrite(state, t0, err)
			}
			continue
		}

		// A read: hit a seeded present key with probability cfg.hitRate, else an
		// absent key. The populations are disjoint, so writes never move the
		// read hit rate.
		var sid string
		if rng.Float64() < cfg.hitRate {
			sid = sids[rng.Intn(len(sids))]
			if measured {
				state.hits.Add(1)
			}
		} else if len(absent) > 0 {
			sid = absent[rng.Intn(len(absent))]
		} else {
			sid = sids[rng.Intn(len(sids))]
		}
		t0 := time.Now()
		_, err := chk.IsRevoked(ctx, sid)
		if measured {
			classifyRead(state, t0, err)
		}
	}
}

// writeID maps a monotonic cursor into the write pool; the pool is large enough
// that it is never exhausted within a window, but the modulo keeps it bounded.
func writeID(ids []string, cursor int64) string {
	if len(ids) == 0 {
		return ""
	}
	return ids[cursor%int64(len(ids))]
}

func classifyRead(state *windowState, t0 time.Time, err error) {
	if err != nil {
		if isTimeout(err) {
			state.readTO.Add(1)
		} else {
			state.readErrs.Add(1)
		}
		return
	}
	state.readOps.Add(1)
	state.readHist.add(int64(time.Since(t0)))
}

func classifyWrite(state *windowState, t0 time.Time, err error) {
	if err != nil {
		if isTimeout(err) {
			state.writeTO.Add(1)
		} else {
			state.writeErrs.Add(1)
		}
		return
	}
	state.writeOps.Add(1)
	state.writeHist.add(int64(time.Since(t0)))
}

// isTimeout classifies a store error as a deadline/timeout cancellation.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled)
}

// resetWriteIDs deletes the first n write IDs produced so the next run starts
// from a clean write-key population. It is a no-op when chk does not implement
// the Resetter interface.
func resetWriteIDs(ctx context.Context, chk checker.Checker, cfg *config, n int64) {
	rs, ok := chk.(checker.Resetter)
	if !ok || n == 0 {
		return
	}
	if int64(len(cfg.writeIDs)) < n {
		n = int64(len(cfg.writeIDs))
	}
	_ = rs.Delete(ctx, cfg.writeIDs[:n])
}

// finalizeLatency turns one histogram and its counts into a LatencyStats.
func finalizeLatency(h histogram, ops int64, wall time.Duration, errs, timeouts int64) LatencyStats {
	qs := h.percentiles([]float64{0.5, 0.9, 0.99}) // microseconds
	return LatencyStats{
		P50ms:      qs[0] / 1000,
		P90ms:      qs[1] / 1000,
		P99ms:      qs[2] / 1000,
		Maxms:      h.max() / 1000,
		Ops:        ops,
		Throughput: float64(ops) / wall.Seconds(),
		Errors:     errs,
		Timeouts:   timeouts,
	}
}

func writeFraction(total, writes int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(writes) / float64(total)
}

func hitFraction(reads, hits int64) float64 {
	if reads == 0 {
		return 0
	}
	return float64(hits) / float64(reads)
}
