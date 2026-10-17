# Order Service

The foundation starts with PostgreSQL and User Service authentication. Run
`docker compose up --build order-service` from the repository root after copying
`.env.example` to `.env` and configuring credentials. The service listens on host
port 8083; `order.v1.HealthService/Check` is public and probes its database.
Credit, Kafka and object storage are not required.

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
The generated mutation/upload handlers remain unimplemented. The internal
participant RPC is not mounted. Image fields in read responses stay empty.

Contributors should read [the architecture decision](../docs/adr/order-aggregate-foundation.md)
and the root locking-snapshot landmine first. Add new operations as files in
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
automatically retried. Creation will use `Repository.Within` and a revision-zero
change to insert the initial Pending Reservation aggregate, audit and status fact.
The foundation supplies this persistence boundary, not a creation operation.

External events use the contracts in `kafka/order/events/v1` (Go package
`pkg/kafkagen/order/events/v1`), as documented in [the Kafka topic map](../kafka/README.md).
Outbox payloads are versioned internal JSON facts; a future relay must translate
them into those Protobuf messages before publishing. Terminal `OrderOutcome`
events drive Credit settlement. Event schemas belong outside the RPC contracts
under `proto/order/v1`.

Adding an operation also means extending its concrete action and allowed fields
where needed, and testing prerequisites and repeat semantics. In particular,
offer creation/update actions must explicitly permit a same-status change in
`NewChange` and `validateFields`; a matching lifecycle status alone never makes a
retry safe. Metadata-only disposition updates must preserve settlement facts.
Do not issue network calls inside transaction callbacks. Future sagas persist
intent, commit, call Credit, and reacquire the lock to verify operation identity
before applying results; add concrete workflow storage and recovery with those
operations after contract alignment.

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
