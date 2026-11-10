# Moderation Service

Reports, appeals and their image evidence (RP1–RP12). See
[the ADR](../docs/adr/moderation-service.md) for the service boundary and how
outcomes reach User Service.

## APIs

| Service | Who |
|---|---|
| `moderation.report.v1.ReportService` | Participants file, read and answer reports; administrators review and close them |
| `moderation.appeal.v1.AppealService` | Affected users appeal decisions; administrators review and close appeals |
| `moderation.v1.HealthService` | Public |

The report and appeal RPCs require a verified User Service access token. Role
gates are in each domain's `authorization.go`, and participant rules are in
its service.

## Layout

- `internal/report`, `internal/appeal`, `internal/evidence`: the domains, each
  with its Connect handler, service and PostgreSQL repository.
- `internal/users`: the User Service client for names and review and warning
  facts (`user.v1.UserInternalService`).
- `internal/orders`: Order's participant check.
- `internal/outbox`: records closed outcomes in the closing transaction and
  relays them to `moderation.outcome_requested`.
- `database/schema`, `database/query`: goose migrations and sqlc queries. Run
  `make sqlc` after changing either; generated code is in `internal/database`.

## Configuration

| Variable | Purpose |
|---|---|
| `PORT`, `INTERNAL_PORT` | Public and internal listeners (8080, 8081) |
| `DATABASE_URL` | PostgreSQL; migrations run on startup unless `RUN_MIGRATIONS=false` |
| `USER_SERVICE_BASE_URL` | User Service's internal listener: signing keys, names and facts |
| `ORDER_SERVICE_BASE_URL` | Order's internal listener; unset fails order reports closed |
| `KAFKA_BROKERS` | Enables the outcome relay; unset keeps outcomes in the outbox |
| `MODERATION_S3_BUCKET`, `MODERATION_S3_ENDPOINT`, `MODERATION_S3_PUBLIC_ENDPOINT` | Evidence storage; unset disables uploads |
| `MODERATION_S3_UPLOAD_TTL`, `MODERATION_S3_DOWNLOAD_TTL`, `AWS_REGION` | Optional storage settings |

## Tests

```sh
make test
# Integration tests apply the migrations to a throwaway database.
TEST_DATABASE_URL=postgres://... make test-integration
```
