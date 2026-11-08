# Content Moderation Service

Content Moderation owns the review moderation workflow: ingesting review submissions from Kafka, assessing them using LLMs, and publishing completion events back to Kafka. The behaviour follows the `moderation.v1` and `review.v1` contracts in [`proto/moderation/v1`](../proto/moderation/v1) and [`proto/review/v1`](../proto/review/v1).

## What runs

`cmd/server` applies the embedded migrations, then runs two listeners and the
Kafka consumers.

| Listener | Default port | Serves | Authentication |
| --- | --- | --- |
| Public | `PORT` (8080) | `review.v1.ReviewModerationService`, `review.v1.ReviewModerationAdminService`, `moderation.v1.ModerationAdminService`, `grpc.health.v1.Health` | User Service JWT |
| Internal | `INTERNAL_PORT` (8081) | `grpc.health.v1.Health` | Istio workload principal in Kubernetes; no app credential |

The internal listener serves only health checks. Kubernetes authenticates the caller with Istio mTLS.

| Topic | Effect |
| --- | --- |
| `users.review_submitted` | Ingests review submissions into the moderation job queue. |

The ingestion consumer shares the `content-moderation-service` group. Redelivery is harmless: ingested jobs are deduplicated on `moderation_request_id`. Database failures stop the subscription, which restarts with backoff and redelivers the uncommitted event.

| Topic | Effect |
| --- | --- |
| `moderation.review_moderation_finished` | Outbox events published after a job completes. |

## Configuration

| Variable | Required | Notes |
| --- | --- | --- |
| `DATABASE_URL` | Yes | CMS's PostgreSQL database. |
| `USER_SERVICE_URL` | Yes | User Service URL for JWT validation. |
| `PORT` | No | Public listener port; defaults to 8080. |
| `INTERNAL_PORT` | Yes | Private listener; required and must differ from `PORT`. |
| `RUN_MIGRATIONS` | No | `true` by default. |
| `KAFKA_BROKERS` | No | Comma-separated. Without it ingestion doesn't run, and jobs stay queued. |
| `CMS_LLM_API_KEY` | No | Provider API key. Without it no LLM assessments run. |
| `CMS_LLM_BASE_URL` | No | Provider base URL; defaults to the configured endpoint. |
| `CMS_WORKER_CONCURRENCY` | No | Worker pool size; defaults to 2. |
| `CMS_JOB_LEASE` | No | Job claim lease duration; defaults to 180s. |
| `CMS_JOB_HEARTBEAT_INTERVAL` | No | Job heartbeat interval; must be less than lease. |
| `CMS_JOB_MAX_ATTEMPTS` | No | Maximum attempts per job; defaults to 5. |
| `CMS_JOB_RETRY_INITIAL_DELAY` | No | Initial retry delay; defaults to 10s. |
| `CMS_JOB_RETRY_MAX_DELAY` | No | Maximum retry delay; defaults to 5m. |
| `CMS_JOB_POLL_INTERVAL` | No | Job poll interval; defaults to 1s. |

## Layout

- `internal/review`: owner-visible and admin job read models.
- `internal/jobs`: moderation job persistence, worker pool, and evaluator chain.
- `internal/llmpolicy`: LLM policy evaluation and result classification.
- `internal/openaiadapter`: OpenAI API client adapter.
- `internal/evaladapter`: Evaluator chain wrapper.
- `internal/config`: Moderation configuration, draft versions, and activation.
- `internal/rpc/<domain>`: Connect handlers. `internal/rpc/shared` maps domain
  errors to Connect codes and `review.v1`/`moderation.v1` ErrorInfo reasons.
- `internal/outbox`: Outbox relay that publishes completion events to Kafka.
- `internal/router`: mounts the public and internal listeners.

## Development

```sh
docker compose up content-moderation-service   # public listener on CMS_PORT (8085)
make test                                      # unit tests
make test-integration                          # needs Docker for testcontainers
make sqlc                                      # after changing database/query or database/schema
```
