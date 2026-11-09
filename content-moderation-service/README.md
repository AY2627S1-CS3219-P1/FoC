# Content Moderation Service

Content Moderation owns the review moderation workflow: ingesting review submissions from Kafka, assessing them using LLMs, and publishing completion events back to Kafka. The behaviour follows the `moderation.v1` and `moderation.review.v1` contracts in [`proto/moderation/v1`](../proto/moderation/v1) and [`proto/moderation/review/v1`](../proto/moderation/review/v1).

See the [review moderation ADR](../docs/adr/review-content-moderation.md) for
the end-to-end sequence, evaluator error mapping, retry decisions, and data
exposure boundaries.

## What runs

`cmd/server` applies the embedded migrations, then runs two listeners and the
Kafka consumers.

| Listener | Default port | Serves | Authentication |
| --- | --- | --- |
| Public | `PORT` (8080) | `moderation.review.v1.ReviewModerationService`, `moderation.review.v1.ReviewModerationAdminService`, `moderation.v1.ModerationAdminService`, `grpc.health.v1.Health` | Health public; other RPCs require User Service JWT |
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
| `USER_SERVICE_BASE_URL` | Yes | User Service URL for JWT validation. |
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
| `CMS_LOGGING_INTERVAL` | No | Interval for operational state logs; defaults to 5m. |

## Layout

- `internal/review`: owner-visible and admin job read models.
- `internal/jobs`: moderation job persistence, worker pool, and evaluator chain.
- `internal/llmpolicy`: LLM policy evaluation and result classification.
- `internal/openaiadapter`: OpenAI API client adapter.
- `internal/evaladapter`: Evaluator chain wrapper.
- `internal/config`: Moderation configuration, draft versions, and activation.
- `internal/rpc/<domain>`: Connect handlers and domain error mapping.
- `internal/outbox`: Outbox relay that publishes completion events to Kafka.
- `internal/router`: mounts the public and internal listeners.

## Development

```sh
docker compose up content-moderation-service   # public listener on CMS_PORT (8086)
make test                                      # unit tests
make test-integration                          # needs TEST_DATABASE_URL pointing at a throwaway database
make sqlc                                      # after changing database/query or database/schema
```

## Initial inference capability profile

The [SoCLaaS API guide](https://soclaas.comp.nus.edu.sg/guides/api.html)
documents Chat Completions and authenticated model discovery. It does not
promise that every model supports every inference parameter or strict schema
output. Model IDs are discovered, not interpreted as capability declarations.
CMS omits all unset parameters, including floating-point fields; explicit zero
is preserved. A configuration must pass a bounded request using its exact
model, parameters and output mode before activation. No unsupported parameter
is silently dropped, and strict schema mode never falls back to JSON mode.

The following bounds are CMS's initial application profile, not a claim about
all provider models. Enable an optional setting only when the selected model
accepts it in the draft test; otherwise unset it and save a new draft.

| Parameter | CMS validation |
| --- | --- |
| temperature / top_p | Finite numbers in [0, 2] / [0, 1] |
| presence_penalty / frequency_penalty | Finite numbers in [-2, 2] |
| max_completion_tokens / max_tokens | Positive integers; mutually exclusive |
| stop | At most four nonempty, unique strings, each at most 256 bytes |
| seed | Nonnegative integer |
| reasoning_effort | Optional low, medium or high; acceptance is model-specific |
| advanced top_k | JSON integer in [1, 1000] |
| advanced min_p / typical_p | JSON numbers in [0, 1] |
| advanced repetition_penalty | JSON number greater than 0 and at most 2 |

Advanced values are numeric JSON scalars of at most 64 bytes. Objects, arrays,
null, booleans, unknown parameter names and application-owned fields are
rejected. The four-name allowlist bounds the object size. Credentials remain
server-side. Raw provider error bodies, refusal text and review text are
excluded from logs and persisted attempt details; stable error classes are
retained.

The browser regression suite intercepts every RPC and tests the real settings
card plus retry rendering. With the frontend dev server running:

```sh
frontend/node_modules/.bin/playwright install chromium --only-shell
MODERATION_TEST_BASE_URL=http://localhost:5173 npm --prefix frontend run test:moderation
```

It checks hydration and saving of every inference setting (including explicit
zero and a large int64 seed), explicit paused-job recovery consent, and newer
retry generations while publication still holds the previous error result.

## Live provider verification

The opt-in SoCLaaS suite uses `SOCLAAS_API_KEY` from the environment and a
throwaway `TEST_DATABASE_URL`. It exercises authenticated discovery, both output
modes, configuration validation/activation, two workers and durable terminal
outboxes. It is excluded from normal integration tests and CI. See the
[live verification command and recorded results](../docs/runbooks/content-moderation.md#live-soclaas-verification-9-november-2026).
