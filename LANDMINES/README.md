# Landmines

This directory records non-obvious failures that can affect future work. Each entry states when the failure occurs, how to recognize it, and how to proceed.

| Entry | Applies when |
| -- | -- |
| [PostGIS does not start on Apple Silicon](postgis-apple-silicon.md) | `uname -m` prints `arm64` and Compose uses `postgis/postgis:18-3.6`. |
| [Recreated services rebuild Go dependencies](go-cache.md) | Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`. |
| [Compose creates a root-owned local state directory](compose-owned-local-directory.md) | Kubernetes bootstrap uses `.local/k8s` after Compose created `.local` as root. |
| [Buf cannot run the TypeScript generator on Windows](buf-generate-windows.md) | `buf generate` runs on native Windows. |
| [Locking a row does not refresh relationship subqueries](locked-row-relationship-snapshots.md) | A PostgreSQL locking query reads relationships that another transaction can change while holding the parent row lock. |
| [Read-only Go caches hide lint failures](sandbox-go-caches.md) | An agent sandbox cannot write the default Go build or golangci-lint caches. |
| [A new service's database is missing on existing Postgres volumes](new-service-database.md) | A service database was added to `docker/postgres/init-dbs.sh` after your `pgdata` volume was created. |
