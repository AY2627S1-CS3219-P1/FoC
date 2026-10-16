# Order Service

The foundation starts with PostgreSQL and User Service authentication. Run
`docker compose up --build order-service` from the repository root after copying
`.env.example` to `.env` and configuring credentials. The service listens on host
port 8083; `order.v1.HealthService/Check` is public and probes its database.
Read-only startup does not require Credit, Kafka or object storage. Creation is
opt-in and uses the configured Credit backend; see the execution section below.

For an existing Postgres volume, the init script does not run again. Create the
new database once before starting Order:

```sh
docker compose exec postgres sh -c 'createdb -U "$POSTGRES_USER" "${ORDER_DB:-order_dev}"'
```

New volumes create `order_dev` automatically. The default Compose database URL
uses the shared Postgres server and a separate Order database. `ORDER_DATABASE_URL`,
`ORDER_DB` and `ORDER_PORT` override those defaults. Go services share the
`go-modules` volume; restarting a container can still rebuild its compiled Go
cache (see the repository cache landmine).

For a host process set `DATABASE_URL`, `USER_SERVICE_BASE_URL`, `APP_ENV=local`
and optionally `PORT`, then run `make run`. User Service URLs must use HTTPS
outside local mode. Startup pings PostgreSQL, applies embedded goose migrations,
then fetches authentication keys with a one minute cancellation-aware retry
budget. Set `RUN_MIGRATIONS=false` for externally managed schema migrations;
`make migrate-up` and `make migrate-down` remain available. Invalid configuration
fails before serving requests. SIGINT/SIGTERM stops acceptance of requests and
allows up to ten seconds for shutdown.

`make test` runs isolated module tests. `make test-integration` runs disposable
PostgreSQL integration coverage and requires Docker. `make sqlc` regenerates the
single Order SQL package; never edit generated files.

The read RPCs are `GetOrder`, `ListOpenOrders`, `ListMyOrders`, `ListOffers` and
admin `ListUserOrders`. HTTP authentication and Connect method authorization run
before contract validation. Unknown/invisible resources return `not_found`;
dependency diagnostics are logged and clients receive generic internal errors.
`CreateOrder` is implemented with opt-in admission and durable recovery; other
mutation/upload handlers remain unimplemented. The internal
participant RPC is not mounted. Image fields in read responses stay empty.

Contributors should read [the architecture decision](../docs/adr/order-aggregate-foundation.md)
and [the proposed Order/Credit contract decision](../docs/adr/order-credit-orchestration.md),
plus the root locking-snapshot landmine first. Credit's owner acknowledged the #136 baseline. The
[conformance handoff](../docs/testing/order-credit-conformance.md) specifies
creation and later financial workflows; live creation requires the authenticated
Credit backend. Add new operations as files in
`internal/order`; use consumer-owned interfaces and domain values rather than
protobuf/sqlc types. `Service` owns read operations; `PostgresRepository` implements
both `Reader` and `Repository`.

For local mutations, construct `NewMutator(repository)` once with the future
operation dependencies and reuse its `Mutate` method: it locks the Order, then loads relationships
in a separate statement, then invokes the operation's builder. The builder must
authorize from the supplied persisted aggregate and relationships, check its
Credit/proof/deadline prerequisites, copy the Order, increment revision once, and
call `NewChange`. Populate related offer changes with their expected statuses.
Return nil for an action-specific recognized repeat. The adapter checks revision
predicates, permitted action fields and mandatory facts, and writes Order, offers,
audit and outbox in one transaction. Propagate every failure; callbacks are never
automatically retried. Creation uses the same transaction/Apply boundary through `WithinCreation` to
insert Pending Reservation, its workflow/key mapping, audit and status fact
atomically.

Adding an operation also means extending its concrete action and allowed fields
where needed, and testing prerequisites and repeat semantics. In particular,
offer creation/update actions must explicitly permit a same-status change in
`NewChange` and `validateFields`; a matching lifecycle status alone never makes a
retry safe. Metadata-only disposition updates must preserve settlement facts.
Do not issue network calls inside transaction callbacks. Creation persists
intent, commits, calls Credit, and reacquires the lock to verify workflow identity
and phase before applying results. Follow this sequence when adding later
financial workflows.

`database/schema` owns goose migrations; `database/query` owns SQL. Run `make sqlc`
(sqlc v1.30.0) after either changes and commit generated `internal/database/orderdb`
output with its sources. Do not edit applied migrations or generated bindings.
Integration tests start disposable PostgreSQL 18 containers by default. Optional
`TEST_DATABASE_URL` must point to a throwaway PostgreSQL 18 database: the tests
**delete its public schema** and exercise migration up/down/up. Run
`GOWORK=off go test -mod=readonly -race -tags=integration ./...` for concurrency
checks. Root `make fmt`, `make lint`, `make buf-lint` and `make buf-generate`
include Order in the normal repository checks.

`internal/images` defines the future object storage boundary; its configurable
fake is test-only. Upload logic will generate stable object keys, inspect storage
observations and enforce image limits, persisting keys rather than temporary URLs.
AWS S3 is the intended backend; evaluate Floci locally when building uploads.
No image tables, signing adapter, SDK, bucket configuration or upload workflow is
part of this foundation.

## Executable creation and recovery

`CreateOrder` now has durable admission, financial intent and recovery. Start with
orders without images: nonempty `item_image_ids` receive `failed_precondition`.
Live creation requires Credit's `Reserve`, `GetReservation` and
`ReleaseReservation` methods plus their service authentication. That backend is
owned separately; this change supplies no production fake. The implementation and its
limits are recorded in the [creation ADR](../docs/adr/order-creation-recovery.md).

Set the following for a host-run Order process:

```sh
export ORDER_CREATION_ENABLED=true
export SUPPLIER_SERVICE_BASE_URL=http://localhost:8082
export CREDIT_SERVICE_BASE_URL=http://localhost:8084
export CREDIT_SERVICE_TOKEN_FILE=/absolute/path/to/order-credit-token
```

The example Credit port must match the actual Credit deployment. The token file
must contain the dedicated Order-to-Credit bearer credential, not a user JWT.
Provision the real credential separately. HTTP URLs require `APP_ENV=local`;
other environments require HTTPS. The file is read on each call; replace it
atomically to rotate the credential. Neither credential type is persisted.

Compose forwards these settings. To enable it, use a local override to mount the
credential file at the configured container path, for example:

```yaml
# compose.override.yaml (keep real local paths and credentials uncommitted)
services:
  order-service:
    volumes:
      - /absolute/path/to/order-credit-token:/run/secrets/order-credit-token:ro
    environment:
      CREDIT_SERVICE_TOKEN_FILE: /run/secrets/order-credit-token
```

Use a host reachable from the Order container for Credit's URL. Mounting a whole
credential directory and atomically replacing its token file supports rotation
without recreating the container; a single-file bind mount retains its original
inode. Do not forward the user credential to Credit or reuse User's signing key.

With admissions disabled (the default), new creations return `unavailable`.
Stored results still replay, and configuring Credit URL/token keeps recovery
running. An existing unresolved creation requires recovery credentials at startup.
Credit downtime does not prevent startup or database health: admitted requests
remain Pending Reservation until reconciled or release is confirmed after expiry.
Supplier is contacted only for new admissions, using the authenticated caller's
bearer token; its location snapshots are retained for recovery.

Create with a client UUID key. Identical canonical retries resume/replay the same
workflow, including after the original deadline. Changed input returns
`already_exists`. The requester may admit ten new Orders per rolling minute;
retries consume no quota. A confirmed reservation returns Awaiting Courier;
unresolved work returns Pending Reservation; a definitive refusal or confirmed
expiry release returns `failed_precondition`. Use `GetOrder` or `ListMyOrders` to
inspect current state. Client cancellation after admission leaves durable work.

Only expiry **before opening** is handled here. Expiry/cancellation after opening,
assignment, terminal settlement and Kafka publication remain follow-ups.

## Test creation without a Credit deployment

These tests run the real Order handler, generated outbound clients and PostgreSQL
persistence against scripted remote Connect servers. Docker must be available;
no production credentials or manually prepared database are required:

```sh
cd order-service
GOCACHE=/tmp/foc-go-build GOWORK=off go test -mod=readonly -race -tags=integration \
  ./tests/creation -count=1 -v -timeout=5m
```

The suite covers successful creation, retained/changed retries, fifty concurrent
duplicates, requester admission limits, unknown Reserve/not_found, expiry during
reservation, release confirmation, stale responses after lease takeover, caller
cancellation and recovery after remote success/local rollback. It restarts the
operation instance against the same database; it does not claim that Credit's
actual backend or every whole-process crash scenario has been tested.

Run `make test-integration` for all Order persistence tests. Schema changes use a
new migration and `make sqlc`; generated bindings must reproduce without changes.
