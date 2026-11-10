# Content Moderation Service

CMS consumes `users.review_submitted`, assesses review text, and publishes
`moderation.review_moderation_finished` through a transactional outbox.
User Service owns review publication and ratings.

See the [ADR](../docs/adr/review-content-moderation.md) for package roles and
reliability guarantees, and the [runbook](../docs/runbooks/content-moderation.md)
for operations.

## Settings

`DATABASE_URL`, `USER_SERVICE_BASE_URL`, `INTERNAL_PORT` and
`CMS_LLM_MODEL_ID` are required. `PORT` defaults to 8080 and must differ from
the internal port. `KAFKA_BROKERS` enables ingestion and outbox delivery.
`CMS_LLM_API_KEY` enables assessment; without it jobs remain queued.
`CMS_LLM_BASE_URL` defaults to `https://soclaas-api.comp.nus.edu.sg/v1`.
The model must be available at that endpoint.

Settings are read once at startup. Restart CMS to change the model, endpoint
or key. The prompt, rubric, response format and inference parameter choices
are defined in code. Jobs contain no configuration references.

Worker defaults: concurrency 2, request timeout 120s, lease 180s, heartbeat 10s,
maximum attempts 5, retry delay 10s to 5m, poll interval 1s, logging interval 5m.
Their `CMS_*` environment names are listed in `.env.template`. Heartbeats must
be shorter than the lease. Each process has its own concurrency limit.

## Packages and APIs

`internal/moderation` owns assessment. `internal/job` owns durable execution,
queries and workers. `internal/openaiadapter` implements the completion port.
`cmd/server` wires them together through consumer-defined interfaces.
`internal/outbox` publishes terminal results.

Public RPCs are the review moderation owner and job admin services, protected
by User Service JWT authentication, plus standard `grpc.health.v1.Health`.
The internal listener serves health only. There is no configuration admin API.

## Development

Copy `.env.template`, supply the model and provider credentials, and use
`docker compose up content-moderation-service` from the repository root.
Generate database adapters with `make sqlc` in this directory.

Run `GOWORK=off go test -mod=readonly ./...` for unit tests. Integration tests
require `TEST_DATABASE_URL` pointing to a throwaway CMS database and run with
`go test -p 1 -tags=integration ./...`. They migrate and clear test data.
