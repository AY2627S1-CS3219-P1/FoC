# Content moderation operations

See the [ADR](../adr/review-content-moderation.md) for the workflow and the
[service README](../../content-moderation-service/README.md) for settings.

## Starting and changing settings

Supply a model ID through `CMS_LLM_MODEL_ID`, a provider endpoint through
`CMS_LLM_BASE_URL`, and a secret through `CMS_LLM_API_KEY`. The model must be
available at the endpoint. Restart CMS after changing these values. There is
no configuration database, seed, admin editor or draft testing API.

Without a provider key CMS keeps accepting submissions but leaves workers
idle. Without Kafka brokers ingestion and completion delivery do not run.
Missing required startup settings cause startup to fail with a validation error.

## Diagnosing stalled work

Check queue depth, oldest queued job, pending outbox count, and worker logs.
`CMS_LOGGING_INTERVAL` controls periodic reports. Health probes check database
availability, so a healthy listener alone does not prove workers are assessing.

A rejected provider key, model or request setting logs that assessment workers
have stopped. Fix the deployment settings and restart the affected processes.
The claim is released and can run with the new process settings. Calls still
count as attempts: repeated failed restarts may require a manual job retry.
Ordinary connection, timeout, server and output errors follow bounded retries.

Use the admin review moderation RPCs to inspect job attempts or retry a terminal
error. Retry requires the expected run generation, fences the previous run and
uses the current process settings. Owners can only view their own job summaries.
Technical failure never publishes an approved verdict.

For pending completion events, inspect Kafka connectivity and outbox relay logs.
Publishing and acknowledgement are separate; duplicates are expected and User
Service deduplicates by event ID. Never manually alter an approved or rejected
review to compensate for a delivery delay.

## Health

Direct CMS probes use `/grpc.health.v1.Health/Check`. The shared Kubernetes
gateway exposes CMS health at `/moderation/grpc.health.v1.Health/Check` and
rewrites the prefix. Other moderation RPC routes require authentication.

## Verification

Use separate throwaway User and CMS databases. Run unit tests in each module,
and CMS integration tests with `TEST_DATABASE_URL` and `-tags=integration -p 1`.
Provider adapter tests use local fake HTTP servers and require no paid key.
The frontend browser check intercepts all RPCs and exercises configuration
removal and newer-generation retry rendering. It does not verify real login,
provider credentials, Kafka or end-to-end publication.

### Repeatable commands

From each service directory, run unit tests and then integration tests against
that service's own throwaway database. Integration suites migrate and may clear
schemas; use a separate database for User and CMS.

```sh
GOWORK=off go test -mod=readonly ./...
GOWORK=off TEST_DATABASE_URL="$TEST_DATABASE_URL" go test -mod=readonly -p 1 \
  -tags=integration ./... -count=1 -timeout=10m
```

With the frontend development server running, the browser regression command is:

```sh
MODERATION_TEST_BASE_URL=http://localhost:5173 npm --prefix frontend run test:moderation
```

### Verification history and limits

The 8 November fake-provider end-to-end smoke and 9 November live SoCLaaS
checks recorded in the earlier runbook tested the former configuration
save/test/activation workflow. They established approval/rejection delivery,
publication filtering and provider compatibility for that earlier code and
model profile. They do not establish live compatibility of the simplified
runtime-settings implementation. The old opt-in configuration suite was removed
with that workflow; its historical command is no longer a current test command.

For the simplified implementation, unit and PostgreSQL integration suites and
the mocked browser checks passed. A fresh live smoke with local SoCLaaS
credentials has not been recorded. Such a check must use the configured model
and JSON object request, real User/CMS binaries and Kafka, and verify pending
submission, terminal outbox delivery, approval/rejection application and rating
counts. A successful health probe alone does not establish those outcomes.

## Recovery details

| Symptom | Action and expected behavior |
| --- | --- |
| Worker crash or shutdown | Owned releases return work to the queue; otherwise lease expiry allows recovery. Old tokens cannot finalize. Repeated releases still consume attempts. |
| Provider configuration rejection | Inspect the sanitized attempt class and worker-stop log, correct process settings and restart every affected replica. Release does not publish a terminal event. |
| Terminal `errored` job | Use admin inspection and retry with the expected generation. Retry increments the generation once; a stale retry fails its guard. |
| Kafka outage | Restore connectivity and inspect consumer/relay logs. Durable User submissions and committed CMS results remain in their respective outboxes. |
| Undecodable Kafka bytes | Subscriber stops and restarts; inspect the bad record and repair the offset deliberately. The typed ingestion handler cannot acknowledge undecodable bytes. |
| Existing local Postgres volume lacks CMS database | Run the idempotent init script below and restart CMS. |

```sh
docker compose exec -T postgres /bin/bash /docker-entrypoint-initdb.d/init-dbs.sh
docker compose restart content-moderation-service
```

Worker database phases have a five-second deadline capped at half the lease.
Detached release cleanup is bounded to two seconds. If cleanup cannot finish,
lease expiry remains the recovery mechanism. A stale completion rolls back its
attempt and outbox changes. The relay publishes outside the claim transaction;
ambiguous delivery can repeat an event with the same ID.

## Local Kubernetes provider key

Supplying `K8S_LLM_API_KEY` on redeployment fills an existing empty
`.local/k8s/secrets/CMS_LLM_API_KEY` file. Existing nonempty keys are preserved.
To rotate a nonempty key, remove only that provider-key file, supply the new
value through `K8S_LLM_API_KEY`, and redeploy. The deployment restarts CMS to
read its new environment. Database passwords and signing keys retain their
existing preservation rules. Do not print keys in logs or command output.

The statistics regression holds the reviewee's row lock while submitting,
rejecting/erroring and removing unpublished reviews. Those operations must
complete without waiting for the ratings lock. Applying approval and removing
an approved review still update the count and average atomically.
