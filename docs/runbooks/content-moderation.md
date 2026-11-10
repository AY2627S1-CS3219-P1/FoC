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
Frontend workflow checks exercise unpublished reviews, job inspection and retry.
