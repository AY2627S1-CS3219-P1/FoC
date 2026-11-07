# pgx served by the benchmark module cache lacks pgcopy and *Conn.Batch

**Applies when:** editing `benchmarks/token-revocation/` (the standalone,
`go.work`-isolated benchmark) and you reach for a bulk-write path in
`internal/infra/populate.go` — `pgcopy.CopyFrom`, `pgxpool.Conn.Batch`, or
`*pgx.Conn.Batch`.

**Symptom:** `go mod tidy` / `go vet` fail with
`module github.com/jackc/pgx/v5@latest found ... but does not contain package
github.com/jackc/pgx/v5/pgcopy`, or `.../Conn has no field or method Batch`,
even though `github.com/jackc/pgx/v5 v5.7.2` is pinned. The module proxy's
extract of pgx v5.7.2 ships the root `CopyFrom` method on `*pgx.Conn` but no
`pgcopy` directory, and `*pgx.Conn` / `*pgxpool.Conn` expose no `Batch` method
in this build.

**Reproduced:** token-revocation benchmark build on 7 Nov 2026 — `go mod tidy`
kept resolving `pgcopy` to a pgx version without the package, and both
`pgxpool.Conn.Batch` and `*pgx.Conn.Batch` were undefined. `go.sum` was
regenerated on a fresh writable module cache (`GOMODCACHE=/tmp/otm`) with
`GOTOOLCHAIN=local`, because the host toolchain is 1.26 (the pinned
`go 1.26.0` directive) and the default sandbox cache was root-owned/read-only.

**Workaround:** bulk-seed Postgres with chunked multi-value `INSERT ... VALUES
($1),($2),…` (5 000 rows per round trip) via a single raw `pgx.Connect`
connection — no `pgcopy`, no `Batch`. The pool-size knob used by the pg/http
checkers (`pgxpool.ParseConfig` + `MaxConns`) is unaffected and is the intended
measurement variable. Do NOT `go get -u github.com/jackc/pgx/v5`; keep the
pinned v5.7.2 or the same packages may vanish again.

**Remove this entry when:** the benchmark is deleted or its pgx requirement is
raised to a version whose served module again contains `pgcopy` and `Batch`.
