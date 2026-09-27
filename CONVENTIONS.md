# Conventions

## Connect RPC

All service APIs, for the frontend and between services, are Connect RPCs.

- API contracts live under `proto/<service>/v1` and use the protobuf package
  `<service>.v1`. Keep service names unique within this repository.
- Buf generates Go messages and Connect handlers under `pkg/gen`, and
  TypeScript messages and service descriptors under `frontend/src/lib/gen`.
  Implementers and callers import generated types, but never edit generated
  files. Protobuf messages are external API contracts; domain and GORM types
  remain internal.
- After changing a contract, run `npm run buf:lint` and
  `npm run buf:generate` from `frontend`. Commit the contract and generated
  output together.
- Handwritten handlers live in the service's `internal/handlers/<domain>`
  package. A handler is a struct that embeds the generated unimplemented
  handler and holds its dependencies as fields (see below). Mount the
  generated handler in the service router.
- Do not add a generic wrapper around generated handlers. Reuse the request
  context for database and network calls; request metadata comes from the
  Connect request or context.
- Handlers translate between protobuf and domain types and map domain errors
  to Connect codes (`connect.NewError`). Business rules stay out of handlers.
- Health RPCs are public. Before mounting a protected RPC, add
  authentication at the HTTP middleware boundary and method-level
  authorization through Connect interceptors.
- Use Connect interceptors for RPC-wide validation, authorization, logging,
  tracing, and error normalization.
- Test RPC behavior once. Handler-mount integration tests use a generated
  Connect client; they do not repeat the same case over native gRPC. Test
  Connect over HTTP/1.1 and native gRPC over h2c together only at the production
  server transport boundary. Add protocol-specific cases elsewhere only when
  behavior differs by protocol.

## Dependencies and interfaces

- Inject dependencies as struct fields, set once in `cmd/server/main.go`
  (the composition root), e.g. `&health.Handler{DB: sqlDB}`. Do not pass
  shared dependencies as per-call function parameters or through a global
  env struct.
- Interfaces are declared by the package that consumes them and list only
  the methods it calls (e.g. a handler's `Logic`, a logic package's
  `UserStore`). Implementations return concrete types; never return an
  interface from a constructor.
- Layers: `internal/handlers/<domain>` (Connect adapter) →
  `internal/<domain>` (business rules and the store interfaces they need) →
  `internal/store` (GORM persistence over `internal/models`).
- Errors are package-level sentinels checked with `errors.Is`. `store`
  returns its own sentinels (`store.ErrNotFound`, ...); logic packages map
  them to domain errors; handlers map domain errors to Connect codes.
- Unit-test logic with fakes of the consumer-defined interfaces; test
  `store` against a real database.

## Database

- `migrations/`: goose migrations, one `0000N_name.sql` per change with
  `-- +goose Up` / `-- +goose Down`. They are embedded in the binary and
  applied on startup unless `RUN_MIGRATIONS=false`. Never edit an applied
  migration. `make migrate-up/down`, `make goose-create name=...`.
- `internal/models`: GORM structs whose tags mirror the SQL. The migrations
  own the schema; do not use `AutoMigrate`.
- `internal/store`: GORM queries. Translate GORM errors (`gorm.ErrRecordNotFound`,
  `gorm.ErrDuplicatedKey`, ...) to `store` sentinels; never let a missing row
  become a 500.
- `internal/models/schema_integration_test.go` runs every migration up, down
  and up again and checks the constraints. It runs only when
  `TEST_DATABASE_URL` points at a throwaway database, because it wipes the schema.

Docs: [goose](https://github.com/pressly/goose),
[GORM](https://gorm.io/docs/),
[Connect](https://connectrpc.com/docs/go/getting-started).

## Legacy REST (supplier-service)

supplier-service still serves REST through `pkg/api` (handlers in
`internal/rest` using `api.HTTPHandler` and `deps.Env`, views in
`internal/views`, errors in `exterrors/errs`) and uses
sqlc, with Connect handlers in `internal/rpc`. Do not add new REST endpoints;
migrate them to Connect handlers following the sections above.

## Run and lint

- `make run` (air live reload), `go build ./...`, `go vet ./...`.
- `.env` needs `DATABASE_URL` (see `.env.template`).
