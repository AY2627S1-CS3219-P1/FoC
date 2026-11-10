# Landmines

This directory records non-obvious failures that can affect future work. Each entry states when the failure occurs, how to recognize it, and how to proceed.

| Entry | Applies when |
| -- | -- |
| [AWS demo validation and logging integration](aws-demo-validation-and-logging.md) | Validating or deploying the AWS demo against current main, especially its read-only Terraform checks on Linux amd64. |
| [EKS worker credits can differ from Terraform state](eks-worker-credit-mode.md) | Creating or replacing the AWS demo's burstable EKS workers from `infra/aws`. |
| [Go line formatting can need multiple passes](golines-multiple-passes.md) | `golangci-lint` v2.14.0 runs `golines` at 120 characters on long boolean expressions. |
| [PostGIS does not start on Apple Silicon](postgis-apple-silicon.md) | `uname -m` prints `arm64` and Compose uses `postgis/postgis:18-3.6`. |
| [Recreated services rebuild Go dependencies](go-cache.md) | Compose replaces a Go service container, including after `docker compose down` or `docker compose up --force-recreate`. |
| [Air can exhaust a shared Linux inotify quota](air-inotify-exhaustion.md) | Multiple development stacks share a Linux host and Air exits before watching files with `too many open files`. |
| [Compose creates a root-owned local state directory](compose-owned-local-directory.md) | Kubernetes bootstrap uses `.local/k8s` after Compose created `.local` as root. |
| [Buf cannot run the TypeScript generator on Windows](buf-generate-windows.md) | `buf generate` runs on native Windows. |
| [Locking a row does not refresh relationship subqueries](locked-row-relationship-snapshots.md) | A PostgreSQL locking query reads relationships that another transaction can change while holding the parent row lock. |
| [Initial navigation callbacks precede shallow-routing readiness](sveltekit-initial-navigation.md) | Frontend code calls `pushState` or `replaceState` during initial hydration, including `afterNavigate` callbacks. |
| [Read-only Go caches hide lint failures](sandbox-go-caches.md) | An agent sandbox cannot write the default Go build or golangci-lint caches. |
| [Reviewer model arguments can differ from runtime](agent-reviewer-model-override.md) | An exact-model review uses an Agent specialization with a model override. |
| [A new service's database is missing on existing Postgres volumes](new-service-database.md) | A service database was added to `docker/postgres/init-dbs.sh` after your `pgdata` volume was created. |
| [Existing Compose environments use User's old public-key address](compose-user-internal-listener.md) | An existing `.env` still sets `USER_SERVICE_BASE_URL=http://user-service:8080` after the User listener split. |
| [Supplier's old Location schema survives on existing Postgres volumes](supplier-edited-migrations.md) | Your `supplier_dev` database applied Supplier migrations before 8 October 2026, or Location requests fail with `column d.ended_at does not exist`. |
| [An old `.env` points Compose services at User Service's public port](compose-user-service-url.md) | Your `.env` sets `USER_SERVICE_BASE_URL=http://user-service:8080`, or Compose services fail with `load authentication keys: unimplemented: 404 Not Found`. |
| [Credit's reservation listener accepts unauthenticated calls](credit-internal-listener-unauthenticated.md) | Credit's `INTERNAL_PORT` is published, exposed through a Service or ingress, or merged into the public listener. |
| [Stack rebases replay integrated merge history](gh-stack-merge-history.md) | `gh stack` v0.1.1 rebases imported branches with merge commits and squash-merged dependencies. |
| [Sandbox blocks local HTTP test servers](sandbox-test-sockets.md) | Go tests use `httptest.NewServer` and the agent sandbox denies local socket creation. |
