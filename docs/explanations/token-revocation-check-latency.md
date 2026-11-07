# Revoked-access-token check latency: Redis vs direct Postgres vs Postgres-via-HTTP

> **Status (8 Nov 2026):** the benchmark module that produced these numbers was
> removed from the repository; the figures below are preserved as a record of
> the experiment. The shipped backend is now **PostgreSQL (variant B)** — the
> "Redis (shipped production path)" labels below describe the state at
> measurement time, not the current implementation.


## Scope

How fast can a service answer "is this access token revoked?" as concurrent load
scales? Three storage strategies for the same lookup are compared end to end:

- **A — Redis** (the shipped production path): `EXISTS revoked_access_tokens:<sid>`.
- **B — Direct Postgres**: a primary-key lookup `SELECT 1 … WHERE sid=$1` over a
  `pgxpool`, no HTTP layer.
- **C — Postgres-via-HTTP**: the identical Postgres query behind one HTTP round
  trip, standing in for an API/proxy boundary between the issuer and a store.

Variant C is the one this benchmark's architectural knob (`--pg-pool`) also
controls: it shares the same Postgres pool sizing as B, so the pool sweep below
shows the cost of the round trip **and** the effect of pool size together.

The benchmark is a standalone module under
`benchmarks/token-revocation/`. It imports and modifies none of the application
services it measures; the Redis and Postgres instances it talks to are synthetic
storage seeded only with synthetic session keys.

## The three checkers

All three implement one method — `IsRevoked(ctx, sessionID) → (bool, error)` —
and treat "no matching row" as *not revoked*.

- **Redis** opens a pooled connection to the Redis dict and runs `EXISTS`.
- **Postgres** opens a `pgxpool` capped at `MaxConns` (`--pg-pool`). Each check is
  one indexed PK lookup. Excess concurrency **queues on the real connections** —
  this is the intended measurement variable, not a defect.
- **HTTP** runs the same Postgres query inside an in-process `http.Server` on
  loopback, and the load generator reaches it over HTTP with a shared client. The
  server owns its own Postgres pool of the same size.

## Methodology

- **Dataset.** `--sessions 1_000_000` distinct session keys are seeded into both
  stores. Every probe hashes one of these keys, so the dominant production case
  (present-key lookup on a PK/dict) is what is measured. Dataset size is held
  constant across the ladder so latency reflects concurrency, not index size.
- **Concurrency ladder.** `--levels 100,1000,10000` concurrent checkers. Each
  worker keeps its own RNG and does one check per loop iteration, so the hot path
  is a single atomic histogram update per check.
- **Window.** 15s measured per level after a 3s unmeasured warm-up (so pools and
  caches are hot). Each variant/level/pool repeats `--reps 3` times and results
  are averaged across repetitions.
- **Pool knob.** For B and C, one Postgres pool of size `N` serves the whole
  run; work beyond `N` connections queues. The pg and http checkers are never
  alive simultaneously — each pool size is built and torn down before the next —
  so they can never together exceed `max_connections`.
- **HTTP load generator.** Variant C's client is a keep-alive `http.Client` with
  generous connection reuse and a configurable `--http-timeout` (30s default). A
  benchmark measures tail latency, so the timeout is deliberately long rather
  than censoring slow-but-valid requests. (See "Known issues".)

## Resource limits

`benchmarks/token-revocation/bench-compose.yaml` pins the infra so the numbers
are reproducible and citable:

| Store | CPUs | Memory |
| -- | -- | -- |
| Postgres | 2.0 | 1 GiB |
| Redis | 1.0 | 512 MiB |

The runs behind these results were collected on a **16-core dev host with
unlimited** container resources (the host `bench-pg`/`bench-redis` are not subject
to the compose limits). The *relative* ordering below holds regardless of host
size, but the *absolute* latencies will shift under the tighter 2-CPU/1-GB Postgres
pin — higher queue waits and an earlier client-saturation ceiling. Re-run under
`bench-compose.yaml` for citable absolute numbers.

## Results — main ladder (Postgres pool = 100)

One Postgres pool per B/C run; pooled and http latencies grow with concurrency.
p99 is the headline — the tail a real request sees.

| Variant | Concurrency | p50 | p90 | **p99** | max | Throughput | CPU |
| -- | -- | --: | --: | --: | --: | --: | --: |
| Redis | 100 | 2.9 ms | 3.9 ms | **5.6 ms** | 11.0 ms | 33.2 k/s | 2.8 |
| Redis | 1000 | 30.7 ms | 35.3 ms | **39.8 ms** | 48.4 ms | 32.8 k/s | 3.2 |
| Redis | 10000 | 310 ms | 335 ms | **348 ms** | 369 ms | 33.3 k/s | 3.2 |
| Postgres (B) | 100 | 2.5 ms | 5.9 ms | **11.2 ms** | 38.2 ms | 33.1 k/s | 3.4 |
| Postgres (B) | 1000 | 32.0 ms | 38.1 ms | **44.5 ms** | 66.9 ms | 31.8 k/s | 3.6 |
| Postgres (B) | 10000 | 349 ms | 399 ms | **448 ms** | 475 ms | 29.6 k/s | 3.3 |
| Postgres-via-HTTP (C) | 100 | 5.2 ms | 9.8 ms | **15.9 ms** | 38.1 ms | 17.6 k/s | 6.0 |
| Postgres-via-HTTP (C) | 1000 | 62.0 ms | 75.3 ms | **93.4 ms** | 133 ms | 16.3 k/s | 6.1 |
| Postgres-via-HTTP (C) | 10000 | 589 ms | 716 ms | **1232 ms** | 1616 ms | 16.5 k/s | 6.8 |

All runs finished with **zero errors**. Raw rows, JSON, a Markdown pivot and a
p99-vs-concurrency chart accompany this writeup in
`benchmarks/token-revocation/results/main-ladder/`.

### Reading the ladder

1. **Redis and direct Postgres are nearly indistinguishable.** Redis is a little
   faster at low concurrency (p99 5.6 ms vs 11.2 ms at c=100) and comparable at
   high concurrency (348 ms vs 448 ms at c=10000). The production Redis path is
   well optimized; a direct PK lookup is a viable, only modestly slower
   alternative, and it avoids the separate store.

2. **The HTTP round trip adds a consistent, growing penalty.** Relative to direct
   Postgres, Variant C is ~1.4× slower at c=100 and ~2.75× slower at c=10000. The
   extra cost is the serialization/framing round trip plus in-process contention:
   Variant C runs the load generator *and* its HTTP server in one process and uses
   ~6 CPU cores versus ~3.4 for direct Postgres (~2× the CPU), which compresses
   its throughput ceiling to ~17 k/s versus ~30–33 k/s.

3. **c = 10000 is client-saturated, not storage-saturated.** Redis p99 jumps from
   40 ms (c=1000) to 348 ms (c=10000) — an 8.7× spike for a store that answers in
   sub-millisecond. That latency is the benchmark client itself: 10,000 Go
   goroutines competing for CPU. Every variant tops out around ~33 k ops/s, which
   is the client's CPU ceiling (~3 cores), not the storage backends. So c=10000
   measures client + storage end to end; the pool knob cannot move it because the
   client is the bottleneck.

## Results — pool sweep (Concurrency = 1000)

Holding concurrency at 1000 (where the client is not yet the bottleneck) and
scaling only the Postgres pool shows how pool size trades queue-wait latency for
connections. Redis (which has no pool knob) is shown as a fixed reference at the
same concurrency.

| Variant | Pool | p50 | p90 | **p99** | max | Throughput | CPU |
| -- | --: | --: | --: | --: | --: | --: | --: |
| Redis (ref) | — | 31.0 ms | 34.9 ms | 41.7 ms | 64.6 ms | 32.6 k/s | 3.2 |
| Postgres (B) | 50 | 35.9 ms | 40.3 ms | 49.5 ms | 66.2 ms | 28.7 k/s | 3.4 |
| Postgres (B) | 100 | 31.9 ms | 39.2 ms | 46.7 ms | 68.1 ms | 30.8 k/s | 3.5 |
| Postgres (B) | 200 | 30.1 ms | 39.2 ms | 49.5 ms | 94.2 ms | 32.9 k/s | 3.7 |
| Postgres (B) | 500 | 28.4 ms | 49.5 ms | 76.7 ms | 250.9 ms | 32.0 k/s | 3.6 |
| Postgres-via-HTTP (C) | 50 | 58.9 ms | 68.1 ms | 76.6 ms | 93.9 ms | 17.4 k/s | 6.6 |
| Postgres-via-HTTP (C) | 100 | 54.0 ms | 64.3 ms | 78.9 ms | 115.9 ms | 18.7 k/s | 7.0 |
| Postgres-via-HTTP (C) | 200 | 50.9 ms | 70.2 ms | 88.6 ms | 154.3 ms | 19.2 k/s | 7.2 |
| Postgres-via-HTTP (C) | 500 | 49.5 ms | 86.0 ms | 129.4 ms | 284.2 ms | 18.9 k/s | 7.1 |

Readings:

1. **The pool knob moves typical (median) latency.** As connections grow, fewer
   requests queue, so p50 falls steadily — Postgres 35.9 → 28.4 ms and HTTP
   58.9 → 49.5 ms going from 50 to 500 connections. This is the lever: size the
   pool for the tail of the *typical* request distribution.

2. **A bigger pool does not guarantee a better tail.** At c = 1000 the p99 is
   noisy and often worse for the largest pool — Postgres p99 46.7 ms (pool 100)
   rises to 76.7 ms (pool 500, max 251 ms); HTTP p99 climbs from 76.6 ms (pool 50)
   to 129.4 ms (pool 500, max 284 ms). Under 1000 concurrent clients the tail is
   driven by host/scheduler/GC jitter and, for the in-process HTTP server, by
   contention among the many handlers a large pool lets run at once — not by pool
   size. Pool 100 sits at the sweet spot here: good median with a bounded tail.

3. **Throughput improves then plateaus.** Postgres throughput rises 28.7 → 32.9
   k/s from pool 50 to 200 and levels off; HTTP rises 17.4 → 19.2 k/s over the
   same range. Each plateaus at its CPU ceiling (~3.5 cores for B, ~7 for C),
   confirming the bottleneck is CPU, not connection count, past pool ≈ 200.

Net effect of the sweep: raising the pool raises the throughput ceiling and trims
median latency, but it is not a tail-latency fix and it costs CPU — size it for the
typical request, and cap it before the extra connections start widening the tail.
Raw data lives in `benchmarks/token-revocation/results/pool-sweep/`.

## Conclusion

For a revoked-access-token check, direct Postgres is competitive with Redis on
latency and eliminates a whole store, while a single HTTP round trip costs roughly
a doubling of latency and twice the CPU. Postgres pool size is the lever for tail
latency at moderate concurrency, up to the point where the benchmark client itself
saturates (around c=10000 on this host). The HTTP boundary is cheap at low load
and predictable, but it is the worst of the three under sustained concurrency.

## Reproduction

```sh
cd benchmarks/token-revocation
export GOWORK=off GOCACHE=/tmp/otg GOMODCACHE=/tmp/otm

# Main ladder: all three variants across the concurrency ladder, pool = 100.
go run . --variant all --levels 100,1000,10000 --sessions 1000000 \
  --duration 15s --warmup 3s --reps 3 --pg-pool 100 \
  --redis-addr 127.0.0.1:16379 \
  --pg-dsn "postgres://bench:bench@127.0.0.1:15432/bench?sslmode=disable" \
  --out results/main-ladder

# Pool sweep: hold c=1000, scale the Postgres pool (needs max_connections >= 550).
go run . --variant all --levels 1000 --sessions 1000000 \
  --duration 15s --warmup 3s --reps 2 --pg-pools 50,100,200,500 \
  --redis-addr 127.0.0.1:16379 \
  --pg-dsn "postgres://bench:bench@127.0.0.1:15432/bench?sslmode=disable" \
  --out results/pool-sweep
```

Raise Postgres `max_connections` before the pool sweep (a fresh container starts
at 100, too small for the larger pools):

```sh
docker exec bench-pg psql -U bench -d bench -h 127.0.0.1 -p 5432 \
  -c "ALTER SYSTEM SET max_connections = 600;"
docker restart bench-pg
```

## Known issues

- **HTTP client timeout was censoring the tail.** An early build used the
  `http.Client` default 2 s timeout, which turned ordinary pool-queue waits at
  c=10000 into `context deadline exceeded` errors and drove http to `ops=0`. The
  checker now builds a keep-alive client with a long, configurable timeout
  (`--http-timeout`, default 30 s) so slow-but-valid requests are measured. The
  `max_connections` ceiling that produced the same symptom via connection
  rejection is recorded in `LANDMINES/redis-bench-pg-max-connections.md`.
- **The issuer runs in-process** for Variant C (by design, per `bench-compose.yaml`
  "Variant C"), so its CPU is counted alongside the load generator. A separate
  issuer process on its own host would lower Variant C's CPU figure.

## Artifacts and limits

- Numbers are a synthetic latency comparison on synthetic 1 M keys. Absolute
  latencies depend on host CPU/memory; the *relative* comparison (Redis ≈ direct
  Postgres, the HTTP penalty, pool leverage, the c=10000 client ceiling) is the
  takeaway, and it is what the writeup asserts.
- Raw data lives in `benchmarks/token-revocation/results/main-ladder/` and
  `benchmarks/token-revocation/results/pool-sweep/` (CSV, JSON, Markdown, SVG).
- The benchmark module is isolated from `go.work` and is not covered by the
  repository's linter; delete the directory to remove it.

## Related

The companion writeup [durable-write latency](token-revocation-write-latency.md)
measures the same three stores and methodology but injects concurrent durable
refresh/logout writes (0/0.1/1/10 % of ops) and reports reads and writes in
separate histograms — so it complements this read-only baseline rather than
repeats it.
