# Access-token revocation under concurrent durable writes: Redis vs Postgres vs Postgres-via-HTTP

## Scope

The companion to
[check latency](token-revocation-check-latency.md), which measured a revocation
check (a read) under read load alone. This document asks the second question a
production revocation path faces: **how do concurrent durable writes — a refresh
or logout that revokes a session — affect the latency of the reads still being
served?**

A revocation *is* a write, and the same middleware checks a huge stream of reads
on every request. So each run is a **mixed workload**: reads probe the seeded
dataset while a configurable fraction of the operations are durable writes
injected into the same three stores:

| Variant | Storage | Read check | **Durable write** |
|---|---|---|---|
| **A — Redis** | Redis dict `revoked_access_tokens:<sid>` | `EXISTS` | `SET <key> <unixts> EX <ttl>` |
| **B — Postgres, direct** | `revoked_access_tokens(sid PK, expires_at)` | `SELECT 1 … WHERE sid=$1 AND expires_at > now()` | `INSERT … VALUES ($1, now()+$2) ON CONFLICT (sid) DO NOTHING` |
| **C — Postgres via HTTP** | same as B, behind one HTTP round trip | `SELECT 1 …` via HTTP | `POST /revoke`, returns only after commit |

The read check is unchanged from the check-latency doc; only the injected write
load differs. A write is **durable** — not a throwaway cache entry. It is only
meaningful to compare stores when the acknowledged write is really flushed:

- **Redis** is gated on `appendonly=yes` **and** `appendfsync=always`, so each
  acknowledged `SET` is fsynced before the client returns.
- **Postgres** is gated on `synchronous_commit=on` and `fsync=on`, so the
  `INSERT` commit is acked only after disk; the table is a logged table.
- **Variant C**'s `/revoke` handler performs that same insert and returns 200
  only after the commit, so its measured write latency is the acknowledged
  durable write end to end.

Every write uses a **unique id** (`write-<n>`). The inserts therefore grow the
key space the way a real revocation stream does; they never overwrite a seeded
read key.

## Methodology (controls shared with the check-latency doc, plus the write-specific ones)

- **Disjoint populations.** Three sets never overlap: **present** keys (real
  UUIDs, seeded into both stores), **absent** keys (real UUIDs, never seeded),
  and **write** keys (`write-0 … write-N`, unique counter ids). Because the write
  pool is disjoint from the read populations, the injected write fraction never
  moves the read hit set.
- **Fixed read hit rate.** Reads sample a seeded present key with probability
  `--read-hit-rate` (default 1 %, i.e. the dominant production case — a
  valid, non-revoked session) and otherwise an absent key. So raising the write
  fraction does not silently change the read distribution.
- **Separate read/write histograms.** The rare but slow durable writes would
  otherwise vanish behind the dominant read traffic. Reads and writes are
  recorded in independent histograms, so each reports its own p50/p90/**p99**,
  max, throughput, and error/timeout counts — and the CSV, the Markdown tables,
  and the two charts keep them apart (no combined histogram).
- **Durable gate.** Before any run that writes, the benchmark verifies the store
  settings and fatals unless they are durable (Redis `appendonly`/`appendfsync`,
  Postgres `synchronous_commit`/`fsync` + logged table). The recorded effective
  settings land in `token-revocation.meta.json`. **Only local durability is
  asserted** — that a single instance flushed the write — not replication or
  failover; a revocation write only needs the single-instance guarantee.
- **Entry TTL.** `--entry-ttl` (default **24 h**) bounds seeded present keys and
  writes. This is a documented deviation from the ~10 min production access-token
  TTL: the benchmark holds every seeded entry valid for the whole sweep so the
  hit rate does not drift as entries lapse. In production, a lapsed entry is
  simply an absent (miss) probe, which the workload already exercises via the
  absent population.
- **Window, reps, client ceiling.** 15 s measured per level after a 3 s
  unmeasured warm-up; `--reps 2` repetitions averaged; each row also reports the
  benchmark process's own CPU, so a row near the container core count flags the
  client (not the store) as the bottleneck.

## Resource limits

`benchmarks/token-revocation/bench-compose.yaml` pins the infra so the numbers
are reproducible and citable. These are the numbers to cite; host runs change
the absolute latencies (see below).

| Store | CPUs | Memory |
| -- | -- | -- |
| Postgres | 2.0 | 1 GiB |
| Redis | 1.0 | 512 MiB |
| Benchmark | 8.0 | 8 GiB |

## Effective configuration (this run)

Recorded in `token-revocation.meta.json`; reproduced here so the run is
self-describing.

| Setting | Value |
| -- | -- |
| Variant | all (redis, pg, http) |
| Concurrency levels | 100, 1000, 10000 |
| Sessions (present population) | 1,000,000 |
| Absent population | 1,000,000 |
| Write-id pool | 500,000 (unique ids) |
| Write fractions | 0, 0.1, 1, 10 % of ops |
| Read hit rate | 1 % (rest absent) |
| Entry TTL | 24 h |
| Duration / warm-up / reps | 15 s / 3 s / 2 |
| Postgres pool | 100 |
| Durability — Redis | `appendonly=yes`, `appendfsync=always` |
| Durability — Postgres | `synchronous_commit=on`, `fsync=on`, logged table (`relpersistence=p`) |

## Results

> These numbers are from a run on a **16-core dev host with unlimited container
> resources** (the host `bench-pg`/`bench-redis` are not subject to the compose
> CPU/memory pins). Absolute latencies shift under the tighter compose limits —
> expect higher queue waits and an earlier client ceiling — but the *relative*
> effect below is the point and holds regardless of host size, exactly as in the
> check-latency doc. Raw rows, CSV, JSON, both SVG charts, and `meta.json` (with
> the verified durability settings) are in `results/write-sweep/`.

The headline question — *do durable writes slow the still-serviced reads?* — has
a clear, store-dependent answer. The table below tracks read p99 at a fixed
c=1000 (where the client is not yet the bottleneck) as the write fraction rises;
the last column is the write-load penalty relative to the read-only baseline.

| Variant | read p99 @ 0% | @ 0.1% | @ 1% | @ 10% | 1% vs 0% |
| -- | --: | --: | --: | --: | --: |
| Redis | 37 ms | 51 ms | **142 ms** | 123 ms | **×3.8** |
| Postgres (direct) | 47 ms | 51 ms | **54 ms** | 56 ms | ×1.2 |
| Postgres-via-HTTP | 86 ms | 86 ms | **81 ms** | 77 ms | ×0.9 (flat) |

Write ack latency (its own histogram, same c=1000) rises less uniformly — it is
the writes that contend, so its pattern is the inverse of interest:

| Variant | write p99 @ 0.1% | @ 1% | @ 10% |
| -- | --: | --: | --: |
| Redis | 56 ms | 133 ms | 120 ms |
| Postgres (direct) | 66 ms | 89 ms | 86 ms |
| Postgres-via-HTTP | 91 ms | 192 ms | 91 ms |

Readings:

1. **Durable writes stall Redis reads hard.** Redis is single-threaded, so the
   read path and the durable write path share one event loop. With
   `appendfsync=always` every acknowledged `SET` runs a full `fsync()` *on that
   loop*, blocking every other client during the call. Read p99 therefore climbs
   ~3.8× going from 0 % to 1 % writes (37 → 142 ms at c=1000), and Redis's total
   throughput collapses with it (34 k/s → ~15 k/s at c=1000): the thread is now
   doing fsyncs, not checks. This is the well-known Redis single-thread stall,
   made visible because the write is genuinely durable here.

2. **Direct Postgres isolates writes from reads.** Reads barely move (47 → 54 ms
   at 1 % writes, ~×1.2). Each write runs on its own backend process and the
   commit/fsync happens there, so readers on other connections are not blocked.
   Postgres is the durable-write-friendly store of the three.

3. **HTTP looks flat — because it is already client-saturated.** Variant C runs on
   ~7 cores at c=1000 and tops out near ~17 k ops/s regardless of write fraction,
   so there is no spare client CPU for the writes to disturb; the writes simply
   ride along. The one blip — write p99 to 192 ms at 1 % writes — reflects the
   in-process contention between the load generator and the `/revoke` handler
   (reps ranged 109–276 ms), not a store effect.

4. **Redis has the lowest durable-write throughput ceiling.** At 10 % writes
   (c=100), Redis sustains ~820 durable writes/s vs ~2 760/s for direct Postgres
   and ~1 770/s for HTTP — because the single thread must `fsync` each write
   itself. If the real revocation stream is write-heavy, a single Redis instance
   is the first to run out of the CPU that both reads and writes compete for.

At c = 10000 all variants hit the same client-CPU ceiling (~34 k ops/s for
Redis/Postgres, ~15–17 k/s for the in-process HTTP), so read p99 jumps to the
500–1 300 ms range for everyone and the write-fraction effect is masked by the
client (a read-only ×3.6 for Redis still shows through: 329 → 1 192 ms). A
handful of read errors (≤0.06 % of ops) appear only in that extreme regime for
HTTP, where client saturation turns ordinary queue waits into `context
deadline` rejections — the same client-ceiling behaviour the check-latency doc
attributes to c = 10000.

Net effect: **durable writes are cheap for direct Postgres reads and costly for
Redis reads**, the opposite of the read-only picture, because the difference
between the stores is how each shares (or does not share) CPU between the read
path and an acknowledged fsync.

## Reproduction

Run against the host `bench-pg`/`bench-redis` (started by the repo's dev
`docker compose`; both already durable — see below):

```sh
cd benchmarks/token-revocation
export GOWORK=off GOCACHE=/tmp/otg GOMODCACHE=/tmp/otm

go run . --variant all \
  --levels 100,1000,10000 --sessions 1000000 \
  --write-percentages 0,0.1,1,10 --write-ids 500000 \
  --read-hit-rate 0.01 --entry-ttl 24h \
  --duration 15s --warmup 3s --reps 2 --pg-pool 100 \
  --redis-addr 127.0.0.1:16379 \
  --pg-dsn "postgres://bench:bench@127.0.0.1:15432/bench?sslmode=disable" \
  --out results/write-sweep
```

Or, on a self-contained stack with the durability pins already set:

```sh
make bench-write            # WRITE_PERCENTAGES defaults to 0,0.1,1,10
make bench-write LEVELS=1000 WRITE_PERCENTAGES=0,0.1,1,10 REPS=3 PG_POOLS=50,100,200
```

Both the host `bench-redis` and `bench-pg` are started durable by the repo dev
compose:

```sh
# bench-redis: append-only, fsync on every acknowledged append
docker exec bench-redis redis-cli CONFIG SET appendonly yes
docker exec bench-redis redis-cli CONFIG SET appendfsync always

# bench-pg: synchronous commit + fsync (compose pins max_connections=600 too,
# so the pool knob does not hit the connection ceiling)
docker exec bench-pg psql -U bench -d bench -h 127.0.0.1 -p 5432 \
  -c "SHOW synchronous_commit; SHOW fsync;"
```

The benchmark verifies these itself and fatals if they are not durable, so the
gate is authoritative regardless of how the instances were started.

## Known issues

- **Variant C is in-process.** The issuer (Variant C) runs inside the benchmark
  process, so its CPU is counted alongside the load generator. A separate issuer
  process on its own host would lower Variant C's CPU figure and, likely, its
  write latency under contention. This is by design, per
  `bench-compose.yaml` ("Variant C"). See the check-latency doc's "Known issues".
- **Local durability only.** The durability gate asserts a single instance
  flushed the write; it does not assert replication or automatic failover. Do
  not read these numbers as a durability/availability claim.

## Artifacts and limits

- Numbers are a synthetic latency comparison on synthetic keys; absolute
  latencies depend on host CPU/memory. The *relative* comparison under load is
  the takeaway — the same caveat as the check-latency doc.
- Raw data, CSV, JSON, Markdown, both SVG charts, and `meta.json` live in
  `benchmarks/token-revocation/results/write-sweep/`.
- The benchmark module is isolated from `go.work` and is not covered by the
  repository's linter; delete the directory to remove it.
