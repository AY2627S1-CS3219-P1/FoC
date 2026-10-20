# Landmines

This directory records non-obvious failures that can affect future work. Each entry states when the failure occurs, how to recognize it, and how to proceed.

| Entry | Applies when |
| -- | -- |
| [PostGIS does not start on Apple Silicon](postgis-apple-silicon.md) | `uname -m` prints `arm64` and Compose uses `postgis/postgis:18-3.6`. |
| [Recreated services rebuild Go dependencies](go-cache.md) | Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`. |
| [Locking a row does not refresh relationship subqueries](locked-row-relationship-snapshots.md) | A PostgreSQL locking query reads relationships that another transaction can change while holding the parent row lock. |
| [Read-only Go caches hide lint failures](sandbox-go-caches.md) | An agent sandbox cannot write the default Go build or golangci-lint caches. |
| [sqlc rejects tuple assignments with named parameters](sqlc-tuple-assignment.md) | sqlc v1.30.0 generates a PostgreSQL update using a tuple assignment from a subquery containing named parameters. |
