# The benchmark's Postgres pool sweep needs a raised `max_connections`

**Applies when:** running `benchmarks/token-revocation/` with a `--pg-pools`
sweep (or `--pg-pool`) larger than the container's `max_connections`, e.g. the
intended `500`-connection pool, against a `bench-pg` started from scratch.

**Symptom:** runs return `err != 0`, or (historically) `http` returns
`ops=0` with `context deadline exceeded`. New Postgres connections beyond
`max_connections` are rejected, so the pgxpool can never reach its configured
size. pg_stat_activity shows well under `max_connections`, so it is not
connection leak or idle-in-transaction; it is a hard rejection on connect.

**Why it is non-obvious:** the pgxpool `MaxConns` is a client-side knob and the
pool "queues" excess work on real connections — it does not tell you the server
has refused the connection. The failure only surfaces once you ask for more
connections than `max_connections` (100 by default) plus headroom.

**Reproduced:** 7 Nov 2026 — the pool sweep to 500 (two coexisting pools during
the pg/http phases) exceeded the default `max_connections=100` and the http
checker collapsed to `ops=0`. Raised to `600` and the sweep and full ladder ran
clean (`err=0` across variants).

**Workaround:** the compose stack now pins the setting, so a fresh
`docker compose -f bench-compose.yaml up` starts Postgres with
`max_connections=600` (via the `postgres -c max_connections=600` command in
`bench-compose.yaml`) and `make bench-*` is safe — no manual step is needed for
`max_connections` anymore. Only a scratch host `bench-pg` started **outside**
compose (e.g. a bare `docker run postgres:18-alpine`) starts at the default 100
and needs:

```sh
docker exec bench-pg psql -U bench -d bench -h 127.0.0.1 -p 5432 \
  -c "ALTER SYSTEM SET max_connections = 600;"
docker restart bench-pg
```

`ALTER SYSTEM` writes `postgresql.auto.conf`, which persists in the `bench-pg`
PostgreSQL **named volume** across container recreates — keep that volume (do not
`docker volume prune` it) or the setting is lost. A fresh
`docker compose -f bench-compose.yaml up` is unaffected (compose sets it). The
pool sweep must satisfy `largest_pool + overhead <= max_connections`.

**Remove this entry when:** the benchmark is deleted, the pool sweep is capped
at or below the default `max_connections`, or `bench-compose.yaml` no longer
sets `max_connections` itself.
