# Conventions

## Git branches

- Use `<owner>/<type>/<description>` with a lowercase kebab-case description.
- Andrew's feature branches use `andrew/feat/<description>`, for example
  `andrew/feat/location-workflows`. Use `fix`, `refactor`, `chore`, or `docs`
  instead of `feat` when appropriate.

## REST handlers
Shape: `func(r *http.Request, env *deps.Env) (*api.Response, error)`.

- App deps come from `env` (`Queries`, `Firebase`, `Pool`). Never take
  `http.ResponseWriter`; the envelope writer owns it.
- Per-request values (auth UID) come from context, e.g.
  `middleware.GetUserUIDFromContext`.
- Return `nil, err` on failure. `ExternalError` sets the status/message;
  anything else becomes 500 with a generic message.

```go
func CreateUser(r *http.Request, env *deps.Env) (*api.Response, error) {
 var req userview.CreateUserView
 if err := api.Decode(r, &req); err != nil {
  return nil, err
 }
 user, err := env.Queries.CreateUser(r.Context(), *req.ToCreateUserParams())
 if err != nil {
  return nil, errors.Wrap(err, "failed to create user")
 }
 return api.NewResponse(userview.ToUserView(&user),
  api.WithCode(http.StatusCreated),
 )
}
```

Register with `api.HTTPHandler(env, Handler)`. Raw bytes/streams bypass the
envelope: `api.NewRawResponse` / `api.NewStreamResponse`. One 15s timeout
(`api.HandlerTimeout`); no per-route timeout middleware.

## REST responses and errors

- Success envelope: `{"status":[{"message","severity"}],"data"}`.
  `severity` is `info|success|warning|error`.
- Decode with `api.Decode(r, &v)`: 1MB cap, unknown fields and trailing data
  rejected, `validator` tags enforced. All failures are 400.
- Views live in `internal/views/<domain>view`, one file per direction
  (`create.go`, `read.go`, `auth.go`). Request structs carry `validate` tags;
  conversion to `sqlc` params lives in `ToXParams` methods.
- Shared external error types live in `pkg/api/errs`: `BadRequest` (400),
  `Unauthorized` (401), `Forbidden` (403), and `NotFound` (404). Keep
  service-specific error values and messages in the service. Wrap with context
  (`WrapXError`); log via `ErrorTrace`.
  Map `pgx.ErrNoRows` to `NotFound`, never 500.

## Connect RPC

All service APIs, for the frontend and between services, are Connect RPCs.

- API contracts live under `proto/<service>/v1` and use the protobuf package
  `<service>.v1`. Keep service names unique within this repository.
- Buf generates Go messages and Connect handlers under `pkg/gen`, and
  TypeScript messages and service descriptors under `frontend/src/lib/gen`.
  Implementers and callers import generated types, but never edit generated
  files. Protobuf messages are external API contracts; domain, sqlc and GORM
  types remain internal.
- After changing a contract, run `npm run buf:lint` and
  `npm run buf:generate` from `frontend`. Commit the contract and generated
  output together.
- Handwritten Go implementations embed the generated unimplemented handler.
  Mount the generated handler in the service router.
  - supplier-service: implementations live under `internal/rpc`. Pass
    application dependencies to each RPC service constructor.
  - user-service: handlers live in `internal/handlers/<domain>`. A handler is
    a struct that holds its dependencies as fields (see "Dependencies and
    interfaces").
- Do not add a generic wrapper around generated handlers. Reuse the request
  context for database and network calls; request metadata comes from the
  Connect request or context.
- Handlers translate between protobuf and domain types and map domain errors
  to Connect codes (`connect.NewError`). Business rules stay out of handlers.
- Health RPCs are public. Before mounting a protected RPC, add
  authentication at the HTTP middleware boundary and method-level
  authorization through Connect interceptors. Generated RPC paths do not
  inherit REST middleware mounted under `/api`.
- Use Connect interceptors for RPC-wide validation, authorization, logging,
  tracing, and error normalization. Keep business logic in shared operations
  when REST and RPC adapters expose the same behavior.
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
- Unit-test logic with fakes of the consumer-defined interfaces; test
  `store` against a real database.

## Database

Supplier Service uses sqlc:

- `database/schema`: goose migrations (`make migrate-up/down`,
  `make goose-create name=...`). `database/query`: sqlc queries.
- After changing either, run `make sqlc`. It generates
  `internal/database/userdb` and `internal/database/seeddb`. Never hand-edit
  generated files.
- `internal/database/utils.go`: `pgtype` converters (`ToPGDate`, ...).

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

User Service uses GORM:

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
[sqlc](https://docs.sqlc.dev/en/stable/reference/config.html),
[GORM](https://gorm.io/docs/),
[Connect](https://connectrpc.com/docs/go/getting-started),
[validator](https://github.com/go-playground/validator),
[pgx](https://github.com/jackc/pgx).

## Run and lint

- `make run` (air live reload), `go build ./...`, `go vet ./...`.
- `.env` needs `DATABASE_URL` (see `.env.template`).
