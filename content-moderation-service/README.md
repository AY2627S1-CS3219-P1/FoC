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
Their environment names and operational effects are listed below. Heartbeats must
be shorter than the lease. Each process has its own concurrency limit.

| Variable | Default / requirement | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | Required | CMS PostgreSQL database. |
| `USER_SERVICE_BASE_URL` | Required | User internal listener for JWT public keys. |
| `PORT` / `INTERNAL_PORT` | `8080` / required | Public and private listeners; ports must differ. |
| `RUN_MIGRATIONS` | `true` | Apply embedded migrations; deployments with a separate migration job disable it. |
| `KAFKA_BROKERS` | Optional | Comma-separated brokers; enables ingestion and completion relay. |
| `CMS_LLM_MODEL_ID` | Required | Model available at the configured endpoint. |
| `CMS_LLM_BASE_URL` | `https://soclaas-api.comp.nus.edu.sg/v1` | OpenAI-compatible provider endpoint. |
| `CMS_LLM_API_KEY` | Optional | Server-side secret; missing key leaves workers idle. |
| `CMS_LLM_REQUEST_TIMEOUT` | `120s` | Deadline for one provider call, including response reads. |
| `CMS_WORKER_CONCURRENCY` | `2` | Maximum concurrent assessments per process. |
| `CMS_JOB_LEASE` | `180s` | Ownership lease; crash recovery resumes after expiry. |
| `CMS_JOB_HEARTBEAT_INTERVAL` | `10s` | Renewal interval; must be shorter than lease. |
| `CMS_JOB_MAX_ATTEMPTS` | `5` | Overall attempt budget per generation. |
| `CMS_JOB_RETRY_INITIAL_DELAY` / `CMS_JOB_RETRY_MAX_DELAY` | `10s` / `5m` | Jittered retry bounds; initial must not exceed maximum. |
| `CMS_JOB_POLL_INTERVAL` | `1s` | Poll interval while no job is available. |
| `CMS_LOGGING_INTERVAL` | `5m` | Periodic queue, outbox and worker reports. |

## Packages and APIs

`internal/moderation` owns assessment. `internal/job` owns durable execution,
queries and workers. `internal/openaiadapter` implements the completion port.
`cmd/server` wires them together through consumer-defined interfaces.
The worker owns `Assessor`; moderation owns `CompletionClient`.
Moderation has no dependency on jobs or persistence. Job persistence stays
colocated with lifecycle rules rather than adding a repository interface to
every SQL operation.
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

## Provider contract and verification

Assessment uses the code-owned rubric and JSON object output, followed by
local schema, verdict and reason-code validation. Optional inference parameters
are omitted so the provider uses its defaults. Model names do not guarantee
capabilities; a provider configuration rejection stops workers until settings
are corrected and CMS restarts. Provider refusal is a technical assessment
failure, never a rejection verdict.

Unit and integration provider tests use local fake HTTP servers. The frontend
browser check intercepts RPCs and verifies configuration removal and retry
rendering; it does not authenticate with a real account or call SoCLaaS.
See the runbook for repeatable commands and the limits of historical live
verification.
