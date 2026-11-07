# Content moderation service runbook

Operational reference for `content-moderation-service` (CMS). Architecture and
rationale: [ADR](../adr/review-content-moderation.md).

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | — (required) | CMS's own `moderation_dev` database. |
| `KAFKA_BROKERS` | — (optional) | Without it CMS runs degraded: no ingestion/relay, workers still drain. |
| `PORT` / `INTERNAL_PORT` | 8080 / required | Public and internal listeners; only the public one is published. |
| `RUN_MIGRATIONS` | `true` | Compose/K8s migrations run separately; disable in those deployments. |
| `USER_SERVICE_URL` | — (required) | User's internal listener for JWT public keys. |
| `CMS_WORKER_CONCURRENCY` | `2` | Evaluation worker goroutines **per process**. Positive integer; replicas multiply. |
| `CMS_LLM_BASE_URL` | `https://soclaas-api.comp.nus.edu.sg/v1` | OpenAI-compatible endpoint. |
| `CMS_LLM_API_KEY` | — (optional) | Server-side secret; never exposed via RPC/UI. |
| `CMS_LLM_REQUEST_TIMEOUT` | `120s` | Bounded inference deadline per request. |
| `CMS_JOB_LEASE` | `180s` | Claim lease; heartbeat must be well below it. |
| `CMS_JOB_HEARTBEAT_INTERVAL` | `10s` | Lease renewal cadence while processing. |
| `CMS_JOB_MAX_ATTEMPTS` | `5` | Attempt budget per run generation. |
| `CMS_JOB_RETRY_INITIAL_DELAY` / `CMS_JOB_RETRY_MAX_DELAY` | `10s` / `5m` | Capped exponential backoff bounds (with jitter). |
| `CMS_JOB_POLL_INTERVAL` | `1s` | Claim-poll pacing when idle. |

Invalid settings fail startup. Heartbeat interval must be smaller than the
lease.

## Degraded modes

- **No API key / no active model configuration**: ingestion continues, jobs
  queue without burning attempts, admin APIs and setup status work. Configure
  a model under Admin → Configuration; queued jobs proceed on the next claim.
- **Kafka unavailable**: User's submission still commits; the review appears
  immediately on the unpublished page. Ingestion/relay resume after the broker
  returns; no submission is lost.
- **Provider outage**: jobs retry with capped backoff; exhausted jobs end
  `errored` and User shows `moderation_error` — never silent forever-pending.

## Recovery

- **Existing Compose pgdata volume without `moderation_dev`** (the database
  was added to `docker/postgres/init-dbs.sh` after the volume was created):
  run the idempotent init script, then restart only CMS. **Never** wipe
  volumes (`docker compose down -v` deletes every service's local data):

  ```sh
  docker compose exec -T postgres /bin/bash /docker-entrypoint-initdb.d/init-dbs.sh
  docker compose restart content-moderation-service
  ```

- **Worker crash**: an expired lease makes the job eligible again with the
  abandoned attempt recorded; a stale worker cannot complete or requeue it.
  An inference repeated after a crash is possible by design.
- **Kafka restarts**: at-least-once redelivery is safe — ingestion dedups on
  `moderation_request_id`, User's consumer dedups on `event_id`.
- **Wire-decode failure** (undecodable bytes on `users.review_submitted`): the
  shared subscriber stops (logged/alerted) and the composition root restarts
  it; repair requires manual offset intervention — the typed handler cannot
  acknowledge bytes it never received.
- **Terminal errored jobs**: an admin retries them (optionally with the
  current configuration) from the admin API; generation increments exactly
  once and older completions cannot overwrite newer results.

## Rollout order

1. Deploy CMS with its database and topics (safe any time; nothing consumes
   events until User is updated).
2. Deploy User so all active readers filter `moderation_status = 'approved'`
   **before** gated pending submissions are accepted. Old User readers must
   not serve reads after pending rows exist.
3. Publish the frontend pages. Configure model + key last; until then the
   system runs in the queued/degraded mode above.

## Observability

Log-based counters follow existing facilities: queue depth and oldest queued
age, in-flight evaluations (≤ `CMS_WORKER_CONCURRENCY` × replicas), attempt
outcomes per class, exhausted jobs, active configuration version, ingestion
and relay failures, outbox lag. Escalate when oldest-queued age grows without
worker progress (usually configuration or provider availability).
