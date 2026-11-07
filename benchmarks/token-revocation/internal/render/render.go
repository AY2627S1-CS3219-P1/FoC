// Package render turns a set of benchmark results into reproducible artifacts:
// a flat CSV, the full raw JSON, a Markdown pivot table (reads and writes
// shown separately), two charts, and a meta.json of provenance. The CSV and
// table average across the repetitions of each variant/concurrency/pool/write-
// pct so the numbers read cleanly; the JSON keeps every raw row. Pure
// formatting — no storage knowledge leaks here.
package render

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/checker"
	"github.com/AY2627S1-CS3219-P1/FoC/benchmarks/token-revocation/internal/load"
)

// Meta is the provenance written to meta.json: what was run, under which limits,
// and with which effective durability settings, so a run is reproducible.
type Meta struct {
	GeneratedAt    string            `json:"generated_at"`
	Question       string            `json:"question"`
	Tool           string            `json:"tool"`
	GoVersion      string            `json:"go_version"`
	Host           string            `json:"host"`
	Limits         Limits            `json:"limits"`
	ClientCPUScope string            `json:"client_cpu_scope"`
	Flags          Flags             `json:"flags"`
	Workload       string            `json:"workload"`
	Durability     map[string]string `json:"durability,omitempty"`
}

// Limits records the pinned CPU/memory limits cited for reproducibility.
type Limits struct {
	Postgres struct {
		CPUs string `json:"cpus"`
		Mem  string `json:"mem"`
	} `json:"postgres"`
	Redis struct {
		CPUs string `json:"cpus"`
		Mem  string `json:"mem"`
	} `json:"redis"`
	Benchmark struct {
		CPUs string `json:"cpus"`
		Mem  string `json:"mem"`
	} `json:"benchmark"`
}

// Flags records the effective CLI flags for the run.
type Flags struct {
	Variant          string  `json:"variant"`
	Levels           string  `json:"levels"`
	Sessions         int     `json:"sessions"`
	AbsentIDs        int     `json:"absent_ids"`
	WritePercentages string  `json:"write_percentages"`
	ReadHitRate      float64 `json:"read_hit_rate"`
	WriteIDs         int     `json:"write_ids"`
	EntryTTL         string  `json:"entry_ttl"`
	Duration         string  `json:"duration"`
	Warmup           string  `json:"warmup"`
	Reps             int     `json:"reps"`
	PGPool           int     `json:"pg_pool"`
	PGPools          string  `json:"pg_pools"`
}

// WriteAll writes results/<name>.csv/.json/.svg/.md into dir and the read
// chart path; it also writes <name>-write.svg (write p99 vs write fraction) and
// <name>.meta.json. The CSV and table average across repetitions; the JSON keeps
// every raw row.
func WriteAll(dir, name string, results []load.Result) (string, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	avg := average(results)
	if err := writeCSV(dir+"/"+name+".csv", avg); err != nil {
		return "", "", err
	}
	raw, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(dir+"/"+name+".json", raw, 0o644); err != nil {
		return "", "", err
	}
	readChart := chartRead(avg)
	if err := os.WriteFile(dir+"/"+name+".svg", []byte(readChart), 0o644); err != nil {
		return "", "", err
	}
	writeChart := chartWrite(avg)
	if err := os.WriteFile(dir+"/"+name+"-write.svg", []byte(writeChart), 0o644); err != nil {
		return "", "", err
	}
	md := markdown(avg, results)
	if err := os.WriteFile(dir+"/"+name+".md", []byte(md), 0o644); err != nil {
		return "", "", err
	}

	return md, dir + "/" + name + ".svg", nil
}

// WriteMeta writes results/<name>.meta.json.
func WriteMeta(dir, name string, m Meta) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(dir+"/"+name+".meta.json", append(b, '\n'), 0o644)
}

type groupKey struct {
	v        string
	writePct float64
	c        int
	p        int
}

// average groups raw rows by variant/write-pct/concurrency/pool and averages the
// measured columns across repetitions, keeping the last rep's session/pool/
// durability metadata for labels. Read and write columns are averaged
// independently so the two workloads never mix.
func average(results []load.Result) []load.Result {
	order := []groupKey{}
	sums := map[groupKey]*load.Result{}
	for _, r := range results {
		k := groupKey{r.Variant, r.WritePct, r.Concurrency, r.Pool}
		if _, ok := sums[k]; !ok {
			sums[k] = &load.Result{
				Variant:     r.Variant,
				Pool:        r.Pool,
				Concurrency: r.Concurrency,
				Sessions:    r.Sessions,
				WritePct:    r.WritePct,
			}
			order = append(order, k)
		}
		s := sums[k]
		avgLatency(&s.Read, r.Read)
		avgLatency(&s.Write, r.Write)
		s.Ops += r.Ops
		s.Throughput += r.Throughput
		s.Errors += r.Errors
		s.Timeouts += r.Timeouts
		s.ClientCPU += r.ClientCPU
		s.AchievedWrite += r.AchievedWrite
		s.AchievedHit += r.AchievedHit
		s.Duration += r.Duration
		s.Rep++
		s.PoolStats = r.PoolStats   // last rep's snapshot
		s.Durability = r.Durability // last rep's settings
		s.ClientSettings = r.ClientSettings
	}
	out := make([]load.Result, 0, len(order))
	for _, k := range order {
		s := sums[k]
		n := float64(s.Rep)
		s.Duration = s.Duration / time.Duration(n)
		s.Throughput /= n
		divideLatency(&s.Read, n)
		divideLatency(&s.Write, n)
		s.Ops = int64(round(float64(s.Ops) / n))
		s.Errors = int64(round(float64(s.Errors) / n))
		s.Timeouts = int64(round(float64(s.Timeouts) / n))
		s.ClientCPU /= n
		s.AchievedWrite /= n
		s.AchievedHit /= n
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Variant != out[j].Variant {
			return variantRank(out[i].Variant) < variantRank(out[j].Variant)
		}
		if math.Abs(out[i].WritePct-out[j].WritePct) > 1e-9 {
			return out[i].WritePct < out[j].WritePct
		}
		return out[i].Concurrency < out[j].Concurrency
	})

	return out
}

// avgLatency accumulates one rep's read or write stats into the running sum.
func avgLatency(s *load.LatencyStats, r load.LatencyStats) {
	s.P50ms += r.P50ms
	s.P90ms += r.P90ms
	s.P99ms += r.P99ms
	s.Maxms += r.Maxms
	s.Ops += r.Ops
	s.Throughput += r.Throughput
	s.Errors += r.Errors
	s.Timeouts += r.Timeouts
}

func divideLatency(s *load.LatencyStats, n float64) {
	s.P50ms /= n
	s.P90ms /= n
	s.P99ms /= n
	s.Maxms /= n
	s.Throughput /= n
	s.Errors = int64(round(float64(s.Errors) / n))
	s.Timeouts = int64(round(float64(s.Timeouts) / n))
}

func round(f float64) float64 { return math.Round(f) }

func variantRank(v string) int {
	switch v {
	case "redis":
		return 0
	case "pg":
		return 1
	case "http":
		return 2
	default:
		return 3
	}
}

func writeCSV(path string, rows []load.Result) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()
	if err := w.Write([]string{
		"variant", "write_pct", "pool", "concurrency", "sessions", "duration_s",
		"achieved_write_pct", "achieved_hit_pct",
		"read_ops", "read_throughput", "read_p50_ms", "read_p90_ms", "read_p99_ms", "read_max_ms", "read_errors", "read_timeouts",
		"write_ops", "write_throughput", "write_p50_ms", "write_p90_ms", "write_p99_ms", "write_max_ms", "write_errors", "write_timeouts",
		"overall_ops", "overall_throughput", "overall_errors", "overall_timeouts",
		"client_cpu",
		"pool_size", "pool_idle", "pool_total_conns", "pool_acquired_conns", "pool_acquire_count", "pool_empty_acquires", "pool_acquire_ms",
		"durable", "client_settings",
	}); err != nil {
		return err
	}
	for _, r := range rows {
		row := []string{
			r.Variant, pctStr(r.WritePct), strconv.Itoa(r.Pool), strconv.Itoa(r.Concurrency), strconv.Itoa(r.Sessions),
			fmt.Sprintf("%.3f", r.Duration.Seconds()),
			pctStr(r.AchievedWrite), pctStr(r.AchievedHit),
			strconv.FormatInt(r.Read.Ops, 10), fmt.Sprintf("%.0f", r.Read.Throughput),
			fmt.Sprintf("%.3f", r.Read.P50ms), fmt.Sprintf("%.3f", r.Read.P90ms), fmt.Sprintf("%.3f", r.Read.P99ms), fmt.Sprintf("%.3f", r.Read.Maxms),
			strconv.FormatInt(r.Read.Errors, 10), strconv.FormatInt(r.Read.Timeouts, 10),
			strconv.FormatInt(r.Write.Ops, 10), fmt.Sprintf("%.0f", r.Write.Throughput),
			fmt.Sprintf("%.3f", r.Write.P50ms), fmt.Sprintf("%.3f", r.Write.P90ms), fmt.Sprintf("%.3f", r.Write.P99ms), fmt.Sprintf("%.3f", r.Write.Maxms),
			strconv.FormatInt(r.Write.Errors, 10), strconv.FormatInt(r.Write.Timeouts, 10),
			strconv.FormatInt(r.Ops, 10), fmt.Sprintf("%.0f", r.Throughput),
			strconv.FormatInt(r.Errors, 10), strconv.FormatInt(r.Timeouts, 10),
			fmt.Sprintf("%.2f", r.ClientCPU),
		}
		if r.PoolStats != nil {
			row = append(row, poolStatStr(r.PoolStats)...)
		} else {
			row = append(row, "", "", "", "", "", "", "")
		}
		dur := "false"
		if r.Durable {
			dur = "true"
		}
		row = append(row, dur, r.ClientSettings)
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return w.Error()
}

func pctStr(f float64) string { return fmt.Sprintf("%.4f", f) }

func poolStatStr(p *checker.PoolStat) []string {
	return []string{
		strconv.Itoa(int(p.Size)), strconv.Itoa(int(p.Idle)), strconv.Itoa(int(p.TotalConns)), strconv.Itoa(int(p.AcquiredConns)),
		strconv.FormatInt(p.AcquireCount, 10), strconv.FormatInt(p.EmptyAcquireCount, 10), fmt.Sprintf("%.1f", p.AcquireDurationMs),
	}
}

// markdown renders pivot tables for reads and writes separately (never a
// combined histogram), plus a short read-vs-write analysis and per-rep write-p99
// variability.
func markdown(rows, raw []load.Result) string {
	var b strings.Builder
	b.WriteString("# Access-token revocation check and durable-write latency: Redis vs Postgres vs Postgres-via-HTTP\n\n")
	b.WriteString("A revocation check (read) with concurrent **durable** refresh/logout writes injected at " +
		"varying fractions, measured against Redis, direct Postgres, and Postgres behind one HTTP round trip. " +
		"Reads and writes are reported in **separate** histograms and percentiles; a combined histogram would let " +
		"the infrequent slow writes vanish behind the dominant read traffic.\n\n")
	b.WriteString("Each row is averaged across `--reps`. Achieved write/hit fractions are the realized mix in the " +
		"measured window (the RNG target may drift under load). Write p99 *reps* column shows the min–max across " +
		"repetitions for one representative concurrency, so the within-group variability is visible alongside the mean.\n\n")

	variants := []string{"redis", "pg", "http"}
	labels := map[string]string{
		"redis": "**Redis dictionary** (`SET revoked_access_tokens:<sid> … EX <ttl>` to write; `EXISTS` to check)",
		"pg":    "**Postgres, direct** (`INSERT … expires_at` to write; `SELECT 1 … WHERE sid=$1 AND expires_at > now()` to check)",
		"http":  "**Postgres via HTTP round trip** (Variant C: same Postgres write/read through one HTTP hop)",
	}
	writeLabel := func(p float64) string {
		if p <= 0 {
			return "reads only"
		}

		return fmt.Sprintf("writes %.1f%%", p*100)
	}

	for _, v := range variants {
		rows := rowsFor(rows, v)
		if len(rows) == 0 {
			continue
		}
		fmt.Fprintf(&b, "## %s\n\n", labels[v])

		// Reads: one block per write fraction, latency/throughput by concurrency.
		writePcts := distinctWritePcts(rows)
		for _, wp := range writePcts {
			rrows := rowsWhereWritePct(rows, wp)
			if len(rrows) == 0 {
				continue
			}
			fmt.Fprintf(&b, "**%s — reads**\n\n", writeLabel(wp))
			b.WriteString("| Concurrency | Read throughput (ops/s) | read p50 (ms) | read p90 (ms) | **read p99** (ms) | read max (ms) | read errors | CPU |\n")
			b.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|\n")
			for _, r := range rrows {
				fmt.Fprintf(&b, "| %d | %.0f | %.3f | %.3f | %.3f | %.3f | %d | %.1f |\n",
					r.Concurrency, r.Read.Throughput, r.Read.P50ms, r.Read.P90ms, r.Read.P99ms, r.Read.Maxms, r.Read.Errors, r.ClientCPU)
			}
			b.WriteString("\n")

			// Writes: same shape, separate table.
			fmt.Fprintf(&b, "**%s — writes (durable ack)**\n\n", writeLabel(wp))
			b.WriteString("| Concurrency | Write throughput (ops/s) | write p50 (ms) | write p90 (ms) | **write p99** (ms) | write max (ms) | write errors | write timeouts |\n")
			b.WriteString("|---:|---:|---:|---:|---:|---:|---:|---:|\n")
			for _, r := range rrows {
				fmt.Fprintf(&b, "| %d | %.0f | %.3f | %.3f | %.3f | %.3f | %d | %d |\n",
					r.Concurrency, r.Write.Throughput, r.Write.P50ms, r.Write.P90ms, r.Write.P99ms, r.Write.Maxms, r.Write.Errors, r.Write.Timeouts)
			}
			b.WriteString("\n")

			// Variability: write p99 min–max across reps at the mid concurrency.
			c := midConcurrency(rrows)
			lo, hi := writeP99Range(raw, v, wp, c)
			if hi > 0 {
				fmt.Fprintf(&b, "*At c=%d, write p99 across reps ranged %.1f–%.1f ms (mean above).*\n\n", c, lo, hi)
			}
		}
	}

	return b.String()
}

func rowsFor(rows []load.Result, v string) []load.Result {
	var out []load.Result
	for _, r := range rows {
		if r.Variant == v {
			out = append(out, r)
		}
	}

	return out
}

func distinctWritePcts(rows []load.Result) []float64 {
	seen := map[float64]bool{}
	var out []float64
	for _, r := range rows {
		if !seen[r.WritePct] {
			seen[r.WritePct] = true
			out = append(out, r.WritePct)
		}
	}
	sort.Float64s(out)

	return out
}

func rowsWhereWritePct(rows []load.Result, wp float64) []load.Result {
	var out []load.Result
	for _, r := range rows {
		if r.Variant == "" {
			continue
		}
		if math.Abs(r.WritePct-wp) > 1e-9 {
			continue
		}
		out = append(out, r)
	}

	return out
}

func midConcurrency(rows []load.Result) int {
	sort.Slice(rows, func(i, j int) bool { return rows[i].Concurrency < rows[j].Concurrency })
	if len(rows) == 0 {
		return 0
	}

	return rows[len(rows)/2].Concurrency
}

// writeP99Range returns the min/max write p99 across raw reps at variant/v,
// write fraction wp, and concurrency c.
func writeP99Range(raw []load.Result, v string, wp float64, c int) (float64, float64) {
	var lo, hi float64
	first := true
	for _, r := range raw {
		if r.Variant != v || math.Abs(r.WritePct-wp) > 1e-9 || r.Concurrency != c {
			continue
		}
		if first {
			lo, hi = r.Write.P99ms, r.Write.P99ms
			first = false

			continue
		}
		if r.Write.P99ms < lo {
			lo = r.Write.P99ms
		}
		if r.Write.P99ms > hi {
			hi = r.Write.P99ms
		}
	}

	return lo, hi
}
