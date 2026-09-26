# Conventions

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
- Errors in `exterrors/errs`: `BadRequest` (400), `Unauthorized` (401),
  `NotFound` (404). Wrap with context (`WrapXError`); log via `ErrorTrace`.
  Map `pgx.ErrNoRows` to `NotFound`, never 500.

## Connect RPC

- API contracts live under `proto/<service>/v1` and use the protobuf package
  `<service>.v1`. Keep service names unique within this repository.
- Buf generates Go messages and Connect handlers under `pkg/gen`, and
  TypeScript messages and service descriptors under `frontend/src/lib/gen`.
  Implementers and callers import generated types, but never edit generated
  files. Protobuf messages are external API contracts; domain and sqlc types
  remain internal.
- After changing a contract, run `npm run buf:lint` and
  `npm run buf:generate` from `frontend`. Commit the contract and generated
  output together.
- Handwritten Go implementations live under the service's `internal/rpc`
  package and embed the generated unimplemented handler. Mount the generated
  handler in the service router.
- Pass application dependencies to each RPC service constructor. Do not add a
  generic wrapper around generated handlers. Reuse the request context for
  database and network calls; request metadata comes from the Connect request
  or context.
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

## Database

- `database/schema`: goose migrations (`make migrate-up/down`,
  `make goose-create name=...`). `database/query`: sqlc queries.
- After changing either, run `make sqlc` to regenerate
  `internal/database/sqlc`. Never hand-edit generated files.
- `internal/database/utils.go`: `pgtype` converters (`ToPGDate`, ...).

Docs: [goose](https://github.com/pressly/goose),
[sqlc](https://docs.sqlc.dev/en/stable/reference/config.html),
[validator](https://github.com/go-playground/validator),
[pgx](https://github.com/jackc/pgx).

## Run and lint

- `make run` (air live reload), `go build ./...`, `go vet ./...`.
- `.env` needs `DATABASE_URL` (see `.env.template`).
