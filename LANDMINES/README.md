# Landmines

This directory records non-obvious failures that can affect future work. Each entry states when the failure occurs, how to recognize it, and how to proceed.

| Entry | Applies when |
| -- | -- |
| [Communication shutdown test can hang during cleanup](communication-shutdown-test.md) | Running Communication's Go tests, especially `TestBotShutdownJoinsStartedWorkers`. |
| [Go line formatting can need multiple passes](golines-multiple-passes.md) | `golangci-lint` v2.14.0 runs `golines` at 120 characters on long boolean expressions. |
| [PostGIS does not start on Apple Silicon](postgis-apple-silicon.md) | `uname -m` prints `arm64` and Compose uses `postgis/postgis:18-3.6`. |
| [Recreated services rebuild Go dependencies](go-cache.md) | Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`. |
| [Air can exhaust a shared Linux inotify quota](air-inotify-exhaustion.md) | Multiple development stacks share a Linux host and Air exits before watching files with `too many open files`. |
| [Buf cannot run the TypeScript generator on Windows](buf-generate-windows.md) | `buf generate` runs on native Windows. |
| [Locking a row does not refresh relationship subqueries](locked-row-relationship-snapshots.md) | A PostgreSQL locking query reads relationships that another transaction can change while holding the parent row lock. |
| [Initial navigation callbacks precede shallow-routing readiness](sveltekit-initial-navigation.md) | Frontend code calls `pushState` or `replaceState` during initial hydration, including `afterNavigate` callbacks. |
| [Read-only Go caches hide lint failures](sandbox-go-caches.md) | An agent sandbox cannot write the default Go build or golangci-lint caches. |
| [Reviewer model arguments can differ from runtime](agent-reviewer-model-override.md) | An exact-model review uses an Agent specialization with a model override. |
| [A new service's database is missing on existing Postgres volumes](new-service-database.md) | A service database was added to `docker/postgres/init-dbs.sh` after your `pgdata` volume was created. |
| [Credit's reservation listener accepts unauthenticated calls](credit-internal-listener-unauthenticated.md) | Credit's `INTERNAL_PORT` is published, exposed through a Service or ingress, or merged into the public listener. |
| [Stack rebases replay integrated merge history](gh-stack-merge-history.md) | `gh stack` v0.1.1 rebases imported branches with merge commits and squash-merged dependencies. |
| [Sandbox blocks local HTTP test servers](sandbox-test-sockets.md) | Go tests use `httptest.NewServer` and the agent sandbox denies local socket creation. |
