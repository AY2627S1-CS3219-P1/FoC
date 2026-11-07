# Token-revocation benchmark: Redis vs PostgreSQL

Standalone latency benchmark for one operation the authentication middleware
performs on every request: **has this access-token session been revoked?** It
answers a single question — *as concurrent load scales, is checking revocation
against Redis, Postgres, or Postgres behind an HTTP hop* — with repeatable
numbers.

It deliberately touches **none** of the application: it imports no service code,
is not listed in `go.work`, and is not in the repo's lint modules, so it is a
self-contained drop-in that can be deleted without affecting anything else.

## What is measured

A revocation check is a single keyed lookup. The three variants implement that
one lookup against three back ends and nothing more (JWT signature verification
and service networking are intentionally out of scope — the goal is to compare
the storage lookup, which is the delta the revocation decision introduces):

| Variant | Storage | Lookup | What it isolates |
|---|---|---|---|
| **A — Redis** | Redis dict `revoked_access_tokens:<sid>` | `EXISTS` | The shipped production path. |
| **B — Postgres, direct** | `revoked_access_tokens(sid PK, revoked_at)` | `SELECT 1 … WHERE sid=$1` | Postgres under the pgx connection-pool constraint. |
| **C — Postgres via HTTP** | same as B, behind one HTTP round trip | `SELECT 1 …` via an HTTP server | The cost of "call the user service" on top of Postgres. |

The dataset is **identical** across variants: one present-key probe per request
(the dominant production case — the overwhelming majority of checks are for
valid, non-revoked sessions). This stresses the Postgres primary-key index at
the chosen size while Redis stays O(1) regardless.

## Durable writes and check latency

The second question the benchmark answers — and the one this extension adds — is
**how concurrent durable writes (a refresh or logout that revokes a session)
affect the check latency of the still-servicing reads.** Production systems do
both at once: a revocation is a write, and the middleware must check a huge
stream of reads on every request. So each run is a **mixed** workload: reads
probe the seeded dataset while `--write-percentages` of the operations are
durable writes injected into the same three stores.

A write is **durable** — it is not a throwaway cache entry. Redis is
`SET <key> … EX <ttl>` with `appendfsync=always`; Postgres is an `INSERT …
expires_at` in a committed transaction with `synchronous_commit=on`; Variant C's
`/revoke` endpoint returns only after that commit. Every write uses a **unique
ID** (`write-<n>`), so it is an insert, never an overwrite of a seeded read key —
the writes therefore grow the key space the way a real revocation stream does,
and they never disturb the read hit set.

The benchmark records **reads and writes as two separate histograms**. A combined
histogram would let the rare but slow durable writes vanish behind the dominant
read traffic; keeping them separate makes their own latency and throughput
headline. Because reads are mostly misses (`--read-hit-rate`, default 1%), and
the write pool is disjoint from the read populations, the read hit rate stays
fixed regardless of how many writes are injected.

Every run is gated on **local durability**: before a durable run it verifies
`appendonly=yes`/`appendfsync=always` on Redis and `fsync=on`/
`synchronous_commit=on` + a logged table on Postgres, and fails loudly if the
running stores are configured to lose acknowledged writes. Only local flush is
asserted — no failover, no replication — so the numbers cite the durable single
instance, which is what a revocation write really needs.

## Methodology (fairness controls)

- **Same dataset, same probes.** Every run seeds the same `--sessions` unique
  keys into both stores (1 000 000 by default) and probes present keys.
- **Fixed measurement window.** Each level warms up (`--warmup`, not measured)
  then runs for `--duration`, recording every latency and error. Throughput is
  ops over the measured wall-clock.
- **Repetitions, median-friendly averaging.** `--reps` repetitions per
  variant/concurrency/pool are averaged for the tables; raw rows are kept in the
  JSON.
- **Fixed, documented resources.** Containers run under pinned CPU/memory limits
  (`bench-compose.yaml`): Postgres 2 vCPU/1 GiB, Redis 1 vCPU/512 MiB, the
  benchmark 8 vCPU/8 GiB. The machine these were produced on is recorded in the
  results doc. Re-running on other hardware changes the absolute numbers; the
  *shape* of the comparison is what generalises.
- **No measurement-window noise.** GC warm-up and seeding happen before the
  window; each worker keeps its own RNG and local max so the hot path is one
  atomic histogram update per check.
- **Honest client ceiling.** Each row reports the benchmark process's own CPU
  use; a row near the container's core count means the client — not the store —
  is the bottleneck at that level.
- **Durable writes only when durable.** Before any run that writes, the benchmark
  checks the effective store settings and fatals unless Redis and Postgres are
  configured so an acknowledged write is really flushed. The recorded effective
  settings land in `token-revocation.meta.json` for provenance.
- **Reads and writes, never mixed.** Latency, throughput, and error counts are
  tracked in separate read and write histograms; the CSV, the Markdown tables,
  and the two charts (`token-revocation.svg` for reads,
  `token-revocation-write.svg` for writes) keep the two workloads apart.
- **Fixed read hit rate.** Reads sample a seeded present key with probability
  `--read-hit-rate` (default 1%), else an absent key, so a higher write
  fraction does not silently change the read distribution.

## Knobs

| Flag | Default | Meaning |
|---|---|---|
| `--variant` | `all` | `redis` \| `pg` \| `http` \| `all` |
| `--levels` | `100,1000,10000` | concurrency ladder |
| `--sessions` | `1000000` | distinct keys seeded per store |
| `--duration` | `15s` | measurement window per level |
| `--warmup` | `3s` | warm-up window per level (excluded) |
| `--reps` | `2` | repetitions per variant/level/pool |
| `--pg-pool` | `100` | pgxpool size for the Postgres variants (the architectural knob) |
| `--pg-pools` | *(none)* | sweep multiple pool sizes, e.g. `50,100,200,500` |
| `--write-percentages` | `0` | fraction of ops that are durable writes; `0` = read-only baseline |
| `--write-ids` | `500000` | size of the unique write-key pool (unique ids => inserts, not overwrites) |
| `--read-hit-rate` | `0.01` | fraction of reads that hit a seeded present key (rest are absent misses) |
| `--entry-ttl` | `24h` | TTL for present keys and writes (must exceed the whole sweep) |

The Postgres **pool size is the architectural variable**: Postgres can only run
as many concurrent lookups as it has real connections, so at concurrency far
above the pool the pgxpool queues and latency rises. Redis multiplexes over a
handful of connections, which is the scaling story the numbers make visible.

## Running

```sh
# full ladder (A, B, C) with the defaults above; raw results -> ./results
make bench

# custom: sweep pool sizes at 1k/10k, 100k sessions, 20s windows, 3 reps
make bench-tune LEVELS=1000,10000 SESSIONS=100000 DURATION=20s REPS=3 PG_POOLS=50,100,200,500

# durable writes: check latency (reads) under concurrent refresh/logout writes
# at 0%, 0.1%, 1%, 10% of ops; separate read/write tables and charts
make bench-write LEVELS=100,1000,10000 WRITE_PERCENTAGES=0,0.1,1,10 REPS=3
```

`make bench` builds the benchmark image, starts Postgres and Redis, runs the
ladder inside a container that talks to both by service name, and tears the
stack down (including the data volume) so each run starts clean. `make bench-write`
does the same but with the compose pins already set to durable Redis and Postgres
(its default `--write-percentages` is `0,0.1,1,10`, which you can override).
Outputs land in `benchmarks/token-revocation/results/`: `token-revocation.csv`,
`token-revocation.json`, `token-revocation.md` (separate read and write pivot
tables), `token-revocation.svg` (read p99-vs-concurrency),
`token-revocation-write.svg` (write p99-vs-write-fraction), and
`token-revocation.meta.json` (effective flags, limits, and durability settings).

## Removing it

The whole module is self-contained. Delete `benchmarks/token-revocation/` and, if
a root `make bench-token-revocation` delegate was added, remove that one line
from the top-level `Makefile`. Nothing in the application references it.
