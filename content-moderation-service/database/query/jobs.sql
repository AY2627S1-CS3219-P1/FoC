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
ON CONFLICT DO NOTHING
RETURNING *;

-- name: ClaimEligibleJob :one
-- Oldest-first among due queued/retry-wait jobs, plus expired-lease recovery.
SELECT j.* FROM moderation_jobs j
WHERE ((
        status IN ('queued','retry_wait')
        AND (next_attempt_at IS NULL OR next_attempt_at <= now())
    )
   OR (status = 'processing' AND lease_until <= now()))
AND NOT EXISTS (
    SELECT 1 FROM moderation_config_pauses p
    WHERE p.version_id::text = COALESCE(j.config_version_id,
        (SELECT active_version_id::text FROM moderation_config_state))
)
ORDER BY created_at ASC, id ASC
FOR UPDATE SKIP LOCKED
LIMIT 1;

-- name: ClaimJob :exec
-- Caller must hold the row (ClaimEligibleJob) or otherwise own the claim.
UPDATE moderation_jobs
SET status = 'processing',
    claim_token = sqlc.arg(claim_token),
    worker_id = sqlc.arg(worker_id),
    lease_until = clock_timestamp() + sqlc.arg(lease_seconds)::double precision * interval '1 second',
    heartbeat_at = now(),
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
SET heartbeat_at = now(),
    lease_until = clock_timestamp() + sqlc.arg(lease_seconds)::double precision * interval '1 second'
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing'
  AND lease_until > clock_timestamp();

-- name: ReleaseJob :one
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
  AND status = 'processing'
  AND lease_until > clock_timestamp()
RETURNING *;

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
  AND status = 'processing'
  AND lease_until > clock_timestamp();

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
  AND status = 'processing'
  AND lease_until > clock_timestamp();

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
  AND status = 'processing'
  AND lease_until > clock_timestamp();

-- name: GetJobsForReviewer :many
SELECT * FROM moderation_jobs
WHERE reviewer_id = sqlc.arg(reviewer_id)
  AND moderation_request_id = ANY(sqlc.slice(moderation_request_ids))
ORDER BY created_at DESC;

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

-- name: ResumeConfigurationPausedJobs :many
UPDATE moderation_jobs j
SET run_generation = run_generation + 1, attempt_count = 0, status = 'queued',
    config_version_id = sqlc.arg(config_version_id), policy_version = sqlc.arg(policy_version),
    next_attempt_at = NULL, outcome = NULL, reason_codes = NULL, error_class = NULL,
    finished_at = NULL, claim_token = NULL, worker_id = NULL,
    lease_until = NULL, heartbeat_at = NULL, updated_at = now()
WHERE j.status IN ('queued', 'retry_wait', 'processing')
  AND EXISTS (SELECT 1 FROM moderation_config_pauses p WHERE p.version_id::text = j.config_version_id)
RETURNING j.id;

-- name: ExhaustJob :execrows
-- The caller holds the row lock obtained by ClaimEligibleJob.
UPDATE moderation_jobs
SET status = 'errored', error_class = 'attempts_exhausted', outcome = NULL,
    reason_codes = NULL, next_attempt_at = NULL, finished_at = now(),
    claim_token = NULL, worker_id = NULL, lease_until = NULL, heartbeat_at = NULL,
    updated_at = now()
WHERE id = sqlc.arg(id) AND run_generation = sqlc.arg(run_generation)
  AND status IN ('queued', 'retry_wait', 'processing');

-- name: LockJob :one
SELECT * FROM moderation_jobs WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockOwnedJob :one
-- Take the job lock before touching attempts, matching completion/recovery order.
SELECT * FROM moderation_jobs
WHERE id = sqlc.arg(id) AND run_generation = sqlc.arg(run_generation)
  AND claim_token = sqlc.arg(claim_token) AND status = 'processing'
  AND lease_until > clock_timestamp()
FOR UPDATE;
