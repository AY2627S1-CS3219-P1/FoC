-- name: GetJobByModerationRequest :one
SELECT * FROM moderation_jobs
WHERE moderation_request_id = sqlc.arg(moderation_request_id);

-- name: GetJobByID :one
SELECT * FROM moderation_jobs
WHERE id = sqlc.arg(id);

-- name: InsertJob :one
INSERT INTO moderation_jobs (
    moderation_request_id, source_event_id, review_id, reviewer_id,
    content_revision, comment, run_generation, status, attempt_count
) VALUES (
    sqlc.arg(moderation_request_id), sqlc.arg(source_event_id), sqlc.arg(review_id),
    sqlc.arg(reviewer_id), sqlc.arg(content_revision), sqlc.arg(comment),
    sqlc.arg(run_generation), sqlc.arg(status), sqlc.arg(attempt_count)
)
RETURNING *;

-- name: Quarantine :exec
INSERT INTO moderation_quarantine (topic, message_key, event_id, reason, payload_summary)
VALUES (
    sqlc.arg(topic), sqlc.arg(message_key), sqlc.narg(event_id),
    sqlc.arg(reason), sqlc.narg(payload_summary)
);

-- name: ClaimEligibleJob :one
-- Oldest-first among due queued/retry-wait jobs, plus expired-lease recovery.
SELECT * FROM moderation_jobs
WHERE (
        status IN ('queued','retry_wait')
        AND (next_attempt_at IS NULL OR next_attempt_at <= now())
    )
   OR (status = 'processing' AND lease_until <= now())
ORDER BY created_at ASC, id ASC
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: ClaimJob :exec
-- Caller must hold the row (ClaimEligibleJob) or otherwise own the claim.
UPDATE moderation_jobs
SET status = 'processing',
    claim_token = sqlc.arg(claim_token),
    worker_id = sqlc.arg(worker_id),
    lease_until = sqlc.arg(lease_until),
    heartbeat_at = sqlc.arg(lease_until),
    attempt_count = attempt_count + 1,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND status <> 'completed'
  AND status <> 'errored';

-- name: PinActiveConfig :exec
-- Pins the effective configuration (and its policy version) before the first
-- inference. Config and policy are pinned together once, then retained across
-- retries of the same generation.
UPDATE moderation_jobs
SET config_version_id = sqlc.arg(config_version_id),
    policy_version = sqlc.arg(policy_version),
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND config_version_id IS NULL;

-- name: HeartbeatJob :execrows
UPDATE moderation_jobs
SET heartbeat_at = sqlc.arg(heartbeat_at),
    lease_until = sqlc.arg(lease_until)
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing';

-- name: ReleaseJob :execrows
-- Returns owned work on shutdown: requeue it so a later cycle retries promptly
-- instead of waiting out the lease. Clearing the claim while the row stays
-- 'processing' would violate processing_is_claimed, so the status moves too.
UPDATE moderation_jobs
SET status = 'queued',
    claim_token = NULL,
    worker_id = NULL,
    lease_until = NULL,
    heartbeat_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing';

-- name: InsertAttempt :exec
INSERT INTO moderation_attempts (
    job_id, run_generation, attempt_number, config_version_id, model_id
) VALUES (
    sqlc.arg(job_id), sqlc.arg(run_generation), sqlc.arg(attempt_number),
    sqlc.narg(config_version_id), sqlc.narg(model_id)
);

-- name: FinishAttempt :exec
-- Success: outcome='completed', no error class. Failure: empty outcome, error
-- class plus a bounded, sanitized diagnostic. Unconditional so one shape serves
-- both; only started-but-unfinished attempts are finished.
UPDATE moderation_attempts
SET finished_at = now(),
    outcome = sqlc.arg(outcome),
    error_class = sqlc.arg(error_class),
    detail = sqlc.narg(detail)
WHERE job_id = sqlc.arg(job_id)
  AND run_generation = sqlc.arg(run_generation)
  AND attempt_number = sqlc.arg(attempt_number)
  AND outcome = '';

-- name: CompleteJob :execrows
-- Fenced: only a live claim of the expected generation may terminal-complete.
-- Clears error_class and next_attempt_at: a retried job carries both from
-- ScheduleJobRetry, and completed_decision_is_present requires a completed row
-- to have no error class.
UPDATE moderation_jobs
SET status = 'completed',
    outcome = sqlc.arg(outcome),
    reason_codes = sqlc.arg(reason_codes),
    policy_version = sqlc.arg(policy_version),
    error_class = NULL,
    next_attempt_at = NULL,
    finished_at = now(),
    claim_token = NULL,
    worker_id = NULL,
    lease_until = NULL,
    heartbeat_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND run_generation = sqlc.arg(run_generation)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing';

-- name: ScheduleJobRetry :execrows
UPDATE moderation_jobs
SET status = 'retry_wait',
    error_class = sqlc.arg(error_class),
    next_attempt_at = sqlc.arg(next_attempt_at),
    claim_token = NULL,
    worker_id = NULL,
    lease_until = NULL,
    heartbeat_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND run_generation = sqlc.arg(run_generation)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing';

-- name: ErrorJob :execrows
UPDATE moderation_jobs
SET status = 'errored',
    error_class = sqlc.arg(error_class),
    finished_at = now(),
    claim_token = NULL,
    worker_id = NULL,
    lease_until = NULL,
    heartbeat_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND run_generation = sqlc.arg(run_generation)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing';

-- name: CountAttemptsByClass :one
SELECT count(*)::int
FROM moderation_attempts
WHERE job_id = sqlc.arg(job_id)
  AND run_generation = sqlc.arg(run_generation)
  AND error_class = sqlc.arg(error_class);

-- name: InsertOutbox :exec
INSERT INTO moderation_outbox (
    event_id, job_id, run_generation, payload_version, payload, available_at
) VALUES (
    sqlc.arg(event_id), sqlc.arg(job_id), sqlc.arg(run_generation),
    sqlc.arg(payload_version), sqlc.arg(payload), sqlc.arg(available_at)
);

-- name: ClaimOutboxRow :one
SELECT * FROM moderation_outbox
WHERE published_at IS NULL
  AND available_at <= now()
ORDER BY created_at ASC, event_id ASC
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: ClaimOutboxLease :one
-- Atomically leases the oldest unpublished, available outbox row so a single
-- relay owns it until it publishes or releases. The subquery's FOR UPDATE SKIP
-- LOCKED guarantees mutual exclusion between relays; the lease_token set here
-- fences MarkOutboxPublished so only the owning relay may publish.
UPDATE moderation_outbox
SET lease_token = gen_random_uuid(),
    lease_until = now() + interval '30 seconds'
WHERE event_id = (
    SELECT event_id
    FROM moderation_outbox
    WHERE published_at IS NULL
      AND available_at <= now()
    ORDER BY created_at ASC, event_id ASC
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
RETURNING *;

-- name: MarkOutboxPublished :execrows
UPDATE moderation_outbox
SET published_at = now(),
    lease_token = NULL,
    lease_until = NULL
WHERE event_id = sqlc.arg(event_id)
  AND lease_token = sqlc.arg(lease_token);

-- name: ReleaseOutboxLease :execrows
UPDATE moderation_outbox
SET lease_token = NULL,
    lease_until = now() - interval '1 second'
WHERE event_id = sqlc.arg(event_id)
  AND lease_token = sqlc.arg(lease_token);

-- name: EnsureConfigStateSingleton :exec
INSERT INTO moderation_config_state (id)
VALUES (true)
ON CONFLICT DO NOTHING;

-- name: GetActiveConfigVersion :one
SELECT active_version_id FROM moderation_config_state;

-- name: InsertConfigVersion :one
INSERT INTO moderation_config_versions (
    model_id, parameters, output_mode, policy_version, provider_fingerprint, created_by
) VALUES (
    sqlc.arg(model_id), sqlc.arg(parameters), sqlc.arg(output_mode),
    sqlc.arg(policy_version), sqlc.arg(provider_fingerprint), sqlc.arg(created_by)
)
RETURNING version_id;

-- name: GetActiveConfigVersionFull :one
SELECT v.* FROM moderation_config_versions v
JOIN moderation_config_state s ON s.active_version_id = v.version_id;

-- name: ActivateConfigVersion :execrows
-- NULL expects no active version; all replacements must name the active version.
UPDATE moderation_config_state
SET active_version_id = sqlc.arg(active_version_id),
    activated_by = sqlc.arg(activated_by),
    activated_at = now(),
    updated_at = now()
WHERE active_version_id IS NOT DISTINCT FROM sqlc.narg(expected_version_id)::uuid;

-- name: GetJobsForReviewer :many
SELECT * FROM moderation_jobs
WHERE reviewer_id = sqlc.arg(reviewer_id)
  AND moderation_request_id = ANY(sqlc.slice(moderation_request_ids))
ORDER BY created_at DESC;

-- name: GetAttempts :many
SELECT * FROM moderation_attempts
WHERE job_id = sqlc.arg(job_id)
  AND run_generation = sqlc.arg(run_generation)
ORDER BY attempt_number ASC;

-- name: RetryErroredJob :execrows
-- Retries a terminal errored job exactly once for the expected generation. The
-- service supplies the config/policy to retain (original) or pin (active), so
-- "retain original" preserves the pinned configuration across retries.
UPDATE moderation_jobs
SET run_generation = run_generation + 1,
    attempt_count = 0,
    status = 'queued',
    config_version_id = sqlc.arg(config_version_id),
    policy_version = sqlc.arg(policy_version),
    next_attempt_at = NULL,
    outcome = NULL,
    error_class = NULL,
    finished_at = NULL,
    claim_token = NULL,
    worker_id = NULL,
    lease_until = NULL,
    heartbeat_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND run_generation = sqlc.arg(run_generation)
  AND status = 'errored';

-- name: InsertAdminAudit :exec
INSERT INTO moderation_admin_audit (action, job_id, config_version_id, actor, detail)
VALUES (
    sqlc.arg(action),
    sqlc.narg(job_id),
    sqlc.narg(config_version_id),
    sqlc.arg(actor),
    sqlc.narg(detail)
);

-- name: GetConfigState :one
-- The singleton moderation config state: the active version pointer and the
-- activation metadata. A zero active_version_id means no configuration has been
-- activated. Callers must first ensure the singleton row exists (see
-- EnsureConfigStateSingleton) so this returns a row rather than "no rows".
SELECT active_version_id, activated_by, activated_at FROM moderation_config_state;

-- name: GetConfigVersionById :one
-- An immutable configuration version, by id. Used to retrieve a persisted draft
-- before it is activated.
SELECT * FROM moderation_config_versions
WHERE version_id = sqlc.arg(version_id);
