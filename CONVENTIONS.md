# Conventions

## Architecture and work tracking

- Issues define outcomes, owners, dependencies and acceptance criteria. Link to
  contracts and architecture decisions rather than copying their implementation details.
- For API changes, read the protobuf contracts under `proto/` on the target branch.
  For database changes, read the owning service's migrations and query definitions.
- Record significant architectural choices and their rationale in `docs/adr/`.
  Read applicable decisions on the PR's base and dependency branches.
- Execution plans under `docs/plans/` describe intended work, not proof that it has
  landed. Verify the target branch before treating a plan as the current structure.

## Go readability

- Separate functions and type declarations with a blank line. Within functions,
  use blank lines between logical steps such as validation, database access,
  conversion and response construction.
- Keep a call, its immediate error check and associated cleanup registration
  together. Use spacing to group related statements, rather than separating
  every line.
- Expand long struct and configuration literals across multiple lines so their
  fields are easy to scan.
- Name boolean helpers as predicates that read naturally at call sites, such as
  `HasParticipant(userID)`, `IsRequestedBy(userID)` and `IsValid()`. Parameter
  names should identify what is being checked.
- In longer files, use brief section comments to identify groups of related
  responsibilities. Within complex functions, mark major phases and explain
  non-obvious constraints. Avoid decorative banners and comments that simply
  restate the code.
- Choose logical spacing deliberately; `gofmt` preserves it but does not add it.

## Go checks

- Use `gofmt` and `golines` for handwritten Go, with a target line width of
  120 characters. Generated files are excluded from formatting.
- Run `make fmt` from the repository root to apply Go formatting. It runs
  `fmt-check` after each pass and stops when the check passes, failing after
  five unsuccessful attempts.
- Run `make fmt-check` to check formatting with `golangci-lint fmt --diff`
  without rewriting files.
- Run `make lint` to validate lint configuration, check formatting and run the
  default golangci-lint correctness linters across `pkg`, `user-service`,
  `supplier-service`, `order-service`, `communication-service` and
  `credit-service`. CI installs the tool
  with the official action, then runs the same target for each module. The
  Makefile pins
  the tool version through `.golangci-lint-version`, also used by the official
  golangci-lint CI action. Service lint configurations symlink to the root
  `.golangci.yaml`. Staticcheck uses its `SA*` correctness diagnostics, not style
  or quick-fix suggestions. Naming and package-documentation warnings remain disabled.
- CI checks do not rewrite files. Fast Go tests use
  `GOWORK=off go test -mod=readonly ./...` within each module so the workspace
  cannot conceal incomplete module dependencies or checksums.

## Git branches

- Use `<owner>/<type>/<description>` with a lowercase kebab-case description.
- Andrew's feature branches use `andrew/feat/<description>`, for example
  `andrew/feat/location-workflows`. Use `fix`, `refactor`, `chore`, or `docs`
  instead of `feat` when appropriate.

## Issues and PRs

- Automatically add the appropriate label(s) when you create an issue or a pull
  request: the service/domain label (for example `order-service`), the type label
  (`type.bug`, `type.enhancement`, `type.chore`, `type.documentation`), and any
  project category label (for example `shared-or-infra`, `ready-for-agent`).
- Determine the available labels dynamically with `gh label list` instead of
  relying on a hard-coded label set, so newly added labels stay usable.
- Link the issue the pull request resolves in its body (for example `Closes #123`).

## REST handlers
Shape: `func(r *http.Request, env *deps.Env) (*api.Response, error)`.

- App deps come from `env` (currently `Pool` for Supplier Service REST health). Never take
  `http.ResponseWriter`; the envelope writer owns it.
- Per-request identity comes from the shared User Service JWT authenticator.
- Return `nil, err` on failure. `ExternalError` sets the status/message;
  anything else becomes 500 with a generic message.

Register with `api.HTTPHandler(env, Handler)`. Raw bytes/streams bypass the
envelope: `api.NewRawResponse` / `api.NewStreamResponse`. One 15s timeout
(`api.HandlerTimeout`); no per-route timeout middleware.

## REST responses and errors

- Success envelope: `{"status":[{"message","severity"}],"data"}`.
  `severity` is `info|success|warning|error`.
- Decode with `api.Decode(r, &v)`: 1MB cap, unknown fields and trailing data
  rejected, `validator` tags enforced. All failures are 400.
- If a REST domain needs request or response views, place them in
  `internal/views/<domain>view` with validation tags on request structs.
- Shared external error types live in `pkg/api/errs`: `BadRequest` (400),
  `Unauthorized` (401), `Forbidden` (403), and `NotFound` (404). Keep
  service-specific error values and messages in the service. Wrap with context
  (`WrapXError`); log via `ErrorTrace`.
  Map `pgx.ErrNoRows` to `NotFound`, never 500.

## Connect RPC

All service APIs, for the frontend and between services, are Connect RPCs.

- API contracts live under `proto/<service>/v1` and use the protobuf package
  `<service>.v1`. A distinct domain API may live under
  `proto/<service>/<domain>/v1` with package `<service>.<domain>.v1` (for example,
  `supplier.location.v1`). Keep service names unique within this repository.
- Buf generates Go messages and Connect handlers under `pkg/gen`, and
  TypeScript messages and service descriptors under `frontend/src/lib/gen`.
  Implementers and callers import generated types, but never edit generated
  files. Protobuf messages are external API contracts; domain, sqlc and GORM
  types remain internal.
- After changing a contract, run `make buf-lint` and
  `make buf-generate` from the repository root (install frontend npm dependencies
  first for the TypeScript generator). The Makefile pins and installs Buf. Commit
  the contract and generated
  output together.
- Handwritten Go implementations embed the generated unimplemented handler.
  Mount the generated handler in the service router.
  - supplier-service: implementations live under `internal/rpc`. Pass
    application dependencies to each RPC service constructor.
  - user-service: handlers live in `internal/handlers/<domain>` and are structs
    that hold dependencies as fields (see "Dependencies and interfaces"). The
    allowed-domain admin feature is grouped by domain under `internal/admin`,
    with its Connect handler, service, and PostgreSQL repository together.
  - credit-service: handlers live under `internal/rpc/<domain>`, with error
    and message conversion in `internal/rpc/shared`. Kafka handlers live in
    `internal/events`.
- Do not add a generic wrapper around generated handlers. Reuse the request
  context for database and network calls; request metadata comes from the
  Connect request or context.
- Handlers translate between protobuf and domain types and map domain errors
  to Connect codes (`connect.NewError`). Business rules stay out of handlers.
- Health RPCs are public. Before mounting a protected RPC, add
  authentication at the HTTP middleware boundary and method-level
  authorization through Connect interceptors. Generated RPC paths do not
  inherit REST middleware mounted under `/api`.
- Treat stale authorization claims after a token-backed user attribute changes
  as an access-token lifecycle concern, not as a page-specific error-recovery
  concern. Do not refresh authentication or clear page state in response to a
  `PermissionDenied` solely to compensate for a role change. Until centralized
  revocation is implemented, changes such as administrator demotion may leave
  the UI's token-derived state stale until the access token expires. The planned
  solution is Redis-backed access-token revocation whenever any user attribute
  stored in the token changes.
- Use Connect interceptors for RPC-wide validation, authorization, logging,
  tracing, and error normalization. Keep business logic in shared operations
  when REST and RPC adapters expose the same behavior.
- Test RPC behavior once. Handler-mount integration tests use a generated
  Connect client; they do not repeat the same case over native gRPC. Test
  Connect over HTTP/1.1 and native gRPC over h2c together only at the production
  server transport boundary. Add protocol-specific cases elsewhere only when
  behavior differs by protocol.

## Kafka messages

- Kafka contracts live separately from RPC contracts under
  `kafka/<service>/events/v1/<topic>.proto`, with package
  `<service>.events.v1`. Declare messages and enums, without RPC services.
- `buf.kafka.gen.yaml` generates Go Protobuf types only, under `pkg/kafkagen`.
  Never edit generated files. Run `make buf-lint` and `make buf-generate-kafka`
  after changing an event contract; commit schemas and generated output together.
  `make buf-generate` generates both RPC and Kafka contracts.
- Producers serialize with `proto.Marshal`; consumers deserialize with
  `proto.Unmarshal`. Each topic has an agreed value message and key documented
  in `kafka/README.md`. Keep event contracts independent of RPC request types.
- `infra/kafka/topics.conf` explicitly maps topics to schemas and owns partition
  and replication settings. Topic names use `<service>.<topic>`; schemas for
  shared types do not create topics. Disable broker topic auto-creation.
- Never reuse removed field numbers; reserve removed fields. Check compatibility
  with `buf breaking` against the target branch before merging contract changes.
- Shared Kafka transport lives in `pkg/kafka`. Construct publishers and
  subscribers in the service composition root and inject them; do not use
  globals or package `init` for registration. Register typed handlers before
  starting the blocking `Run(ctx)` loop and monitor its returned error.
- Handlers return success only after their local transaction commits. The
  wrapper commits Kafka offsets afterward and stops on handler/decode failure.
  Services own transactional deduplication, outboxes, saga replies and
  compensation; a business rejection can enqueue a failure event and return
  success. See `pkg/kafka/README.md` for lifecycle and delivery guarantees.

## Dependencies and interfaces

- Inject dependencies as struct fields, set once in `cmd/server/main.go`
  (the composition root), e.g. `&health.Handler{DB: sqlDB}`. Do not pass
  shared dependencies as per-call function parameters or through a global
  env struct.
- Interfaces are declared by the package that consumes them and list only
  the methods it calls (e.g. a handler's `Logic`, a logic package's
  `UserStore`). Implementations return concrete types; never return an
  interface from a constructor.
- Existing GORM-backed User Service domains use these layers:
  `internal/handlers/<domain>` (Connect adapter) → `internal/<domain>`
  (business rules and the store interfaces they need) → `internal/store`
  (GORM persistence over `internal/models`). New sqlc-backed domains may keep
  their repository with the domain package when that keeps the feature
  cohesive.
- Unit-test logic with fakes of the consumer-defined interfaces; test
  `store` against a real database.
- Supplier and Order use domain packages with consumer-owned read/transaction
  interfaces and colocated Postgres adapters. Order keeps its aggregate in one
  `internal/order` package; see `docs/adr/order-aggregate-foundation.md`.

## Database

Use sqlc for new backend services and newly introduced persistence domains.
Existing GORM-backed domains remain on GORM unless an intentional migration is
part of the change; extending one should follow its current persistence
approach.

Supplier, Order and Credit Services use sqlc:

- `database/schema`: goose migrations (`make migrate-up/down`,
  `make goose-create name=...`). `database/query`: sqlc queries.
- After changing either, run `make sqlc`. It generates
  Supplier's `internal/database/seeddb`, `locationdb`, and `idempotencydb`. Never
  hand-edit generated files. Order generates one aggregate package,
  `internal/database/orderdb`, from `order-service/database/sqlc.yaml`. Credit
  generates `internal/database/creditdb` from `credit-service/database/sqlc.yaml`.
- `internal/database/utils.go`: `pgtype` converters (`ToPGDate`, ...).

Order and Communication also use sqlc. Order keeps `sqlc.yaml` in `database/`
and generates `internal/database/orderdb`; Communication keeps it at the service
root, reads `migrations` and `queries`, and generates
`internal/database/communicationdb`. Run `make sqlc` in the service directory.

## Frontend services

- Keep each domain's service, API interface, and Connect adapter together under
  `frontend/src/lib/services/<domain>/`. Use `service.svelte.ts` for rune state;
  a domain `index.ts` may expose a factory but must not create a singleton.
- Compose singleton services in `frontend/src/lib/services/index.ts`. Routes
  and components import services from `$lib/services`. When services depend on
  one another, keep the dependency one-way and inject it from the composition
  root, preferably through an interface owned by the consuming service; do not
  import another service's singleton directly.
- Keep `frontend/src/lib/connect/transport.ts` domain agnostic. It accepts a
  base URL and optional interceptors. Auth RPCs use a plain transport; the
  composition root adds `authService.interceptor` to protected transports.
- Use mutable singletons only in browser lifecycle and event code, never in
  server load functions, actions, hooks, or endpoints. Omit `.ts` suffixes on
  `$lib` imports; relative imports may use `.ts` suffixes.
- Handle unauthenticated access to protected RPCs at the transport layer: the
  auth interceptor refreshes and retries, then the redirect interceptor sends
  the user to `/login`. Pages never check authentication; they redirect only
  for authorization failures (role checks).

## Frontend UI
- Use Superforms to validate and manage form
- Use Svelte-M3 UI components
- Style M3 components through public props, CSS variables, or application-owned
  wrappers and data attributes. Avoid selectors that depend on internal library
  classes (such as `.m3-container`) or the component's internal DOM structure.
- Place components used by a single route next to that page (for example
  `routes/admin/config/AllowedDomainsCard.svelte`). Promote a component to
  `frontend/src/lib/components/<domain>/` only when a second route consumes it
  or it has an app-wide role (layout-level components such as `SnackbarHost`).
  When no page is left in a `lib/components/<domain>/` group, delete the folder.

User Service has legacy GORM-backed domains and may use sqlc for new domains:

- `migrations/`: goose migrations, one `0000N_name.sql` per change with
  `-- +goose Up` / `-- +goose Down`. They are embedded in the binary and
  applied on startup unless `RUN_MIGRATIONS=false`. Never edit an applied
  migration. `make migrate-up/down`, `make goose-create name=...`.
- Existing GORM domains use `internal/models` structs whose tags mirror the
  SQL. The migrations own the schema; do not use `AutoMigrate`.
- Existing GORM domains use `internal/store` for queries. Translate GORM errors
  (`gorm.ErrRecordNotFound`, `gorm.ErrDuplicatedKey`, ...) to `store`
  sentinels; never let a missing row become a 500.
- New sqlc domains keep queries under `database/query`, generated packages
  under `internal/database`, and repositories in their owning domain package.
  Run `make sqlc` after changing queries or the sqlc configuration, and never
  edit generated files.
- `internal/models/schema_integration_test.go` runs every migration up, down
  and up again and checks the constraints. It runs only when
  `TEST_DATABASE_URL` points at a throwaway database, because it wipes the schema.

Docs: [goose](https://github.com/pressly/goose),
[sqlc](https://docs.sqlc.dev/en/stable/reference/config.html),
[GORM](https://gorm.io/docs/),
[Connect](https://connectrpc.com/docs/go/getting-started),
[validator](https://github.com/go-playground/validator),
[pgx](https://github.com/jackc/pgx).

## Run and lint

- `make run` (air live reload), `go build ./...`, `go vet ./...`.
- `.env` needs `DATABASE_URL` (see `.env.template`).
