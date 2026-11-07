// Command token-revocation-benchmark compares how fast a revoked access-token
// session can be checked against three storage strategies as concurrent load
// scales — a Redis dictionary lookup (the shipped production path), a direct
// PostgreSQL primary-key lookup, and the same PostgreSQL lookup behind one HTTP
// round trip — and, once the read picture is known, how concurrent durable
// writes (refresh/logout) affect that check latency. It is a standalone module
// (see benchmarks/token-revocation/) and imports or modifies none of the
// application services it measures.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/checker"
	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/infra"
	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/issuer"
	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/load"
	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/render"
)

func mustEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}

	return def
}

func fatal(what string, err error) { log.Fatal(fmt.Sprintf("%s: %v", what, err)) }

// parseLevels splits a comma list of positive integers.
func parseLevels(s string) ([]int, error) {
	var out []int
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid concurrency %q", part)
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no concurrency levels")
	}

	return out, nil
}

// poolList returns the Postgres pool sizes to sweep for the pg/http variants:
// a single default when no sweep is given, otherwise the explicit list.
func poolList(def int, sweep string) []int {
	if strings.TrimSpace(sweep) == "" {
		return []int{def}
	}
	var out []int
	for _, part := range strings.Split(sweep, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n <= 0 {
			fatal("pg-pools", fmt.Errorf("invalid pool size %q", part))
		}
		out = append(out, n)
	}

	return out
}

// variants expands "all" into the ordered variant list.
func variants(only string) []string {
	if only != "all" {
		return []string{only}
	}

	return []string{"redis", "pg", "http"}
}

// workload carries the data and knobs shared by every run of one checker: the
// disjoint present (hits), absent (misses) and write-key populations, the entry
// TTL, the read hit rate, the timing windows, and the durable-write metadata.
type workload struct {
	sids           []string
	absentIDs      []string
	writeIDs       []string
	entryTTL       time.Duration
	hitRate        float64
	duration       time.Duration
	warmup         time.Duration
	durable        bool
	durability     map[string]string
	clientSettings string
}

func main() {
	var (
		variant       = flag.String("variant", "all", "redis|pg|http|all")
		levels        = flag.String("levels", "100,1000,10000", "comma-separated concurrency ladder")
		sessions      = flag.Int("sessions", 1_000_000, "distinct present (revoked) keys seeded into each store")
		absentIDsFlag = flag.Int("absent-ids", 0, "absent (miss) read population size; 0 == --sessions")
		writePcts     = flag.String("write-percentages", "0", "comma list of write percentages (0..100); 10 => 10% writes; 0 => read-only baseline")
		readHitRate   = flag.Float64("read-hit-rate", 0.01, "fraction of reads that hit a seeded present key (the rest are absent misses)")
		writeIDsFlag  = flag.Int("write-ids", 500_000, "size of the unique write-key pool (unique ids => inserts, not overwrites)")
		entryTTL      = flag.Duration("entry-ttl", 24*time.Hour, "TTL for seeded present keys and writes; must exceed the whole sweep")
		duration      = flag.Duration("duration", 15*time.Second, "measurement window per level")
		warmup        = flag.Duration("warmup", 3*time.Second, "warm-up window per level (not measured)")
		reps          = flag.Int("reps", 2, "repetitions per variant/pool/level/write-pct")
		redisAddr     = flag.String("redis-addr", mustEnv("REDIS_ADDR", "redis:6379"), "Redis address")
		dsn           = flag.String("pg-dsn", mustEnv("PG_DSN", "postgres://bench:bench@postgres:5432/bench?sslmode=disable"), "Postgres DSN")
		pgPool        = flag.Int("pg-pool", 100, "Postgres pool size for pg/http variants (the architectural knob)")
		pgPools       = flag.String("pg-pools", "", "sweep pool sizes, e.g. 50,100,200,500 (empty uses --pg-pool)")
		issuerPort    = flag.String("issuer-port", mustEnv("ISSUER_PORT", "8089"), "issuer HTTP port for Variant C")
		httpTimeout   = flag.Duration("http-timeout", 30*time.Second, "per-request timeout for the HTTP load generator (Variant C)")
		timeout       = flag.Duration("ready-timeout", 120*time.Second, "how long to wait for infra to be reachable")
		out           = flag.String("out", "results", "output directory for csv/json/svg/md/meta.json")
	)
	flag.Parse()

	writePctFrac, err := infra.ParseWritePercentages(*writePcts)
	if err != nil {
		fatal("write-percentages", err)
	}
	hasWrites := false
	for _, p := range writePctFrac {
		if p > 0 {
			hasWrites = true

			break
		}
	}
	absentN := *absentIDsFlag
	if absentN == 0 {
		absentN = *sessions
	}

	ctx := context.Background()

	log.Printf("Redis   : %s", *redisAddr)
	log.Printf("Postgres: %s (pool size %d)", *dsn, *pgPool)
	log.Printf("Write percentages (fraction of ops): %v  Read hit rate: %.1f%%  Entry TTL: %s", writePctFrac, *readHitRate*100, *entryTTL)

	if err := infra.WaitRedis(ctx, *redisAddr, *timeout); err != nil {
		fatal("wait for redis", err)
	}
	log.Printf("Redis reachable")
	if err := infra.WaitPG(ctx, *dsn, *timeout); err != nil {
		fatal("wait for postgres", err)
	}
	log.Printf("Postgres reachable")

	levelsList, err := parseLevels(*levels)
	if err != nil {
		fatal("levels", err)
	}
	pools := poolList(*pgPool, *pgPools)
	log.Printf("Concurrency ladder: %v  Pool sizes: %v  Sessions: %d  Absent: %d  Write-id pool: %d  Reps: %d",
		levelsList, pools, *sessions, absentN, *writeIDsFlag, *reps)

	// Make the table what the write-aware benchmark needs (expires_at) before
	// seeding, whether or not this run writes.
	if err := infra.EnsureSchema(ctx, *dsn); err != nil {
		fatal("ensure schema", err)
	}

	// When any run writes durably, the store durability settings must actually
	// be durable, or the "durable write" premise is false. Gate hard here.
	var durability map[string]string
	if hasWrites {
		settings, err := infra.VerifyDurability(ctx, *redisAddr, *dsn)
		if err != nil {
			fatal("verify durability", err)
		}
		if problems := infra.CheckDurability(settings); problems != "" {
			fatal("durable benchmark requires durable stores; current settings:\n  "+problems, nil)
		}
		durability = settings
		log.Printf("Durability verified: %v", settings)
	} else {
		log.Printf("write-percentages all zero: read-only baseline, skipping durability gate")
	}

	// Three disjoint populations so writes never touch the read hit set:
	// present (real UUIDs, seeded), absent (real UUIDs, never seeded), writes
	// (unique counter ids, so a write is an insert, never an overwrite).
	log.Printf("Generating %d present, %d absent, %d write keys", *sessions, absentN, *writeIDsFlag)
	sids := make([]string, *sessions)
	for i := range sids {
		sids[i] = infra.SessionID()
	}
	absent := make([]string, absentN)
	for i := range absent {
		absent[i] = infra.SessionID()
	}
	writeIDs := make([]string, *writeIDsFlag)
	for i := range writeIDs {
		writeIDs[i] = fmt.Sprintf("write-%d", i)
	}

	// Seed only the present (hit) population, far in the future so entries stay
	// valid for the whole sweep.
	log.Printf("Seeding %d present sessions into Redis and Postgres", *sessions)
	if err := infra.Populate(ctx, *redisAddr, *dsn, sids, *entryTTL); err != nil {
		fatal("populate", err)
	}

	w := workload{
		sids:           sids,
		absentIDs:      absent,
		writeIDs:       writeIDs,
		entryTTL:       *entryTTL,
		hitRate:        *readHitRate,
		duration:       *duration,
		warmup:         *warmup,
		durable:        hasWrites,
		durability:     durability,
		clientSettings: clientSettings(*httpTimeout),
	}

	// Small correctness checks the store actually behaves as the assumptions
	// require: present key revoked, absent key not, and a freshly written
	// revocation is visible. Runs once per variant, before the timed sweep.
	for _, v := range variants(*variant) {
		chk, stop, err := buildVariant(ctx, v, pools[0], *redisAddr, *dsn, *issuerPort, *httpTimeout)
		if err != nil {
			fatal("build "+v, err)
		}
		log.Printf("Correctness checks for %s", v)
		if err := correctness(ctx, chk, v, w); err != nil {
			stop()
			fatal("correctness "+v, err)
		}
		if stop != nil {
			stop()
		}
	}

	var results []load.Result
	for _, v := range variants(*variant) {
		// Variant A (Redis) has no Postgres pool knob, so it runs once against a
		// single logical pool size (0).
		poolsForVariant := pools
		if v == "redis" {
			poolsForVariant = []int{0}
		}
		for _, pool := range poolsForVariant {
			chk, stop, err := buildVariant(ctx, v, pool, *redisAddr, *dsn, *issuerPort, *httpTimeout)
			if err != nil {
				fatal("build "+v, err)
			}
			log.Printf("Running %s pool=%d across write %% %v", v, pool, writePctFrac)
			rows := runVariant(ctx, chk, v, pool, writePctFrac, levelsList, *reps, w)
			results = append(results, rows...)
			if stop != nil {
				stop()
			}
			log.Printf("Done %s pool=%d", v, pool)
		}
	}

	md, svgPath, err := render.WriteAll(*out, "token-revocation", results)
	if err != nil {
		fatal("write results", err)
	}
	meta := buildMeta(w, writePctFrac, durability, levelsList, pools, *variant, *reps)
	if err := render.WriteMeta(*out, "token-revocation", meta); err != nil {
		fatal("write meta", err)
	}
	log.Printf("Results written to %s/ (csv, json, svg, md, meta.json). Markdown summary: %d rows.", *out, len(results))
	_ = md
	log.Printf("Chart: %s", svgPath)
}

// clientSettings records the load-generator/conn settings for provenance.
func clientSettings(httpTimeout time.Duration) string {
	return fmt.Sprintf("http_client_timeout=%s", httpTimeout)
}

// buildVariant constructs a checker for variant v whose Postgres pool size is
// exactly pool. The returned stop releases the checker's resources (and, for
// http, stops the in-process issuer). Building per pool size is deliberate: it
// makes the pool-size sweep real and keeps a single Postgres pool live at a
// time, so pg and http never together exceed max_connections.
func buildVariant(
	ctx context.Context,
	v string,
	pool int,
	redisAddr, dsn, issuerPort string,
	httpTimeout time.Duration,
) (checker.Checker, func(), error) {
	switch v {
	case "redis":
		c, err := checker.NewRedis(ctx, redisAddr)
		if err != nil {
			return nil, nil, err
		}

		return c, func() { _ = c.Close() }, nil
	case "pg":
		c, err := checker.NewPG(dsn, pool)
		if err != nil {
			return nil, nil, err
		}

		return c, func() {
			log.Printf("closing postgres checker pool=%d", pool)
			_ = c.Close()
		}, nil
	case "http":
		addr := "127.0.0.1:" + issuerPort
		stop, statsFn, err := issuer.Start(ctx, dsn, pool, addr)
		if err != nil {
			return nil, nil, err
		}

		// A load-optimized client (keep-alives + generous timeout) so the HTTP
		// side measures server latency instead of choking on connection churn.
		// statsFn feeds the checker's pool-wait accounting. The url is the
		// issuer root; the checker appends /check, /revoke and /delete per call.
		return checker.NewHTTP(checker.NewHTTPClient(httpTimeout), "http://"+addr, statsFn), stop, nil
	}

	return nil, nil, fmt.Errorf("unknown variant %q", v)
}

// runVariant runs the full sweep for one checker: for each write percentage,
// each level, and each repetition, it records a Result, attaches pool-wait
// stats where available, and logs a one-line summary.
func runVariant(
	ctx context.Context,
	chk checker.Checker,
	v string,
	pool int,
	writePcts []float64,
	levels []int,
	reps int,
	w workload,
) []load.Result {
	var out []load.Result
	for _, wp := range writePcts {
		for _, level := range levels {
			for rep := 0; rep < reps; rep++ {
				res := load.Run(ctx, chk,
					load.WithVariant(v),
					load.WithPool(pool),
					load.WithConcurrency(level),
					load.WithSids(w.sids),
					load.WithAbsent(w.absentIDs),
					load.WithWriteIDs(w.writeIDs),
					load.WithWritePct(wp),
					load.WithHitRate(w.hitRate),
					load.WithEntryTTL(w.entryTTL),
					load.WithDuration(w.duration),
					load.WithWarmup(w.warmup),
					load.WithDurable(w.durable, w.durability),
					load.WithClientSettings(w.clientSettings),
				)
				if sp, ok := chk.(checker.StatProvider); ok {
					res.PoolStats = sp.PoolStats()
				}
				res.Rep = rep
				out = append(out, res)
				logResult(res)
			}
		}
	}

	return out
}

// correctness verifies the store matches the workload assumptions, once per
// variant. It probes a present key (revoked), an absent key (not revoked), and
// writes a fresh id then reads it back (visible), then removes that id.
func correctness(ctx context.Context, chk checker.Checker, name string, w workload) error {
	present := w.sids[0]
	absent := w.absentIDs[0]

	if v, err := chk.IsRevoked(ctx, present); err != nil || !v {
		return fmt.Errorf("%s: expected present key %q to be revoked (got %v, err %v)", name, present, v, err)
	}
	if v, err := chk.IsRevoked(ctx, absent); err != nil || v {
		return fmt.Errorf("%s: expected absent key %q to not be revoked (got %v, err %v)", name, absent, v, err)
	}
	sample := fmt.Sprintf("verify-%s-%d", name, time.Now().UnixNano())
	if err := chk.Revoke(ctx, sample, w.entryTTL); err != nil {
		return fmt.Errorf("%s: write sample: %w", name, err)
	}
	v, err := chk.IsRevoked(ctx, sample)
	if err != nil || !v {
		return fmt.Errorf("%s: written revocation %q not visible (got %v, err %v)", name, sample, v, err)
	}
	if rs, ok := chk.(checker.Resetter); ok {
		_ = rs.Delete(ctx, []string{sample})
	}

	return nil
}

func logResult(r load.Result) {
	log.Printf("[%s pool=%d wp=%.3f c=%d rep=%d] read: ops=%d thr=%.0f p50=%.2fms p90=%.2fms p99=%.2fms max=%.2fms err=%d to=%d | write: ops=%d thr=%.0f p50=%.2fms p90=%.2fms p99=%.2fms max=%.2fms err=%d to=%d | achieved wp=%.3f hit=%.3f cpu=%.1f",
		r.Variant, r.Pool, r.WritePct, r.Concurrency, r.Rep,
		r.Read.Ops, r.Read.Throughput, r.Read.P50ms, r.Read.P90ms, r.Read.P99ms, r.Read.Maxms, r.Read.Errors, r.Read.Timeouts,
		r.Write.Ops, r.Write.Throughput, r.Write.P50ms, r.Write.P90ms, r.Write.P99ms, r.Write.Maxms, r.Write.Errors, r.Write.Timeouts,
		r.AchievedWrite, r.AchievedHit, r.ClientCPU)
}

// cpuMem is a CPU/memory limit pair, mirrored from bench-compose.yaml.
type cpuMem struct {
	CPUs string `json:"cpus"`
	Mem  string `json:"mem"`
}

// buildMeta assembles the provenance recorded in meta.json. The limits mirror
// bench-compose.yaml; they are the numbers to cite.
func buildMeta(w workload, writePcts []float64, durability map[string]string, levels, pools []int, variant string, reps int) render.Meta {
	hosts, _ := os.Hostname()
	pcts := make([]string, len(writePcts))
	for i, p := range writePcts {
		pcts[i] = fmt.Sprintf("%.1f", p*100)
	}
	levelsStr := ""
	for i, l := range levels {
		if i > 0 {
			levelsStr += ","
		}
		levelsStr += strconv.Itoa(l)
	}
	poolsStr := ""
	for i, p := range pools {
		if i > 0 {
			poolsStr += ","
		}
		poolsStr += strconv.Itoa(p)
	}
	workloadStr := fmt.Sprintf("reads hit a seeded present key with prob %.1f%%, else an absent key", w.hitRate*100)
	if len(w.writeIDs) > 0 {
		workloadStr += "; writes use unique keys " + w.writeIDs[0] + " … (pool " + strconv.Itoa(len(w.writeIDs)) + ")"
	}
	return render.Meta{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Question:    "How do concurrent durable revocation writes affect revocation-check latency: Redis vs direct Postgres vs Postgres-via-HTTP?",
		Tool:        "token-revocation-benchmark",
		GoVersion:   runtime.Version(),
		Host:        hosts,
		Limits: render.Limits{
			Postgres:  cpuMem{"2.0", "1g"},
			Redis:     cpuMem{"1.0", "512m"},
			Benchmark: cpuMem{"8.0", "8g"},
		},
		ClientCPUScope: "benchmark process (for Variant C, includes the in-process issuer)",
		Flags: render.Flags{
			Variant:          variant,
			Levels:           levelsStr,
			Sessions:         len(w.sids),
			AbsentIDs:        len(w.absentIDs),
			WritePercentages: strings.Join(pcts, ","),
			ReadHitRate:      w.hitRate,
			WriteIDs:         len(w.writeIDs),
			EntryTTL:         w.entryTTL.String(),
			Duration:         w.duration.String(),
			Warmup:           w.warmup.String(),
			Reps:             reps,
			PGPool:           pools[0],
			PGPools:          poolsStr,
		},
		Workload:   workloadStr,
		Durability: durability,
	}
}
