# Conventions

## Handlers

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

## Responses and errors

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
