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
SELECT j.* FROM moderation_jobs j
WHERE ((
        status IN ('queued','retry_wait')
        AND (next_attempt_at IS NULL OR next_attempt_at <= now())
    )
   OR (status = 'processing' AND lease_until <= now()))
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

-- name: HeartbeatJob :execrows
UPDATE moderation_jobs
SET heartbeat_at = now(),
    lease_until = clock_timestamp() + sqlc.arg(lease_seconds)::double precision * interval '1 second'
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing'
  AND lease_until > clock_timestamp();

-- name: ReleaseJob :one
-- Requeue while clearing ownership to preserve processing_is_claimed.
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
-- Fence on a live claim; clear retry state to satisfy completed_decision_is_present.
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
UPDATE moderation_jobs
SET run_generation = run_generation + 1,
    attempt_count = 0,
    status = 'queued',
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

-- name: GetJobViewByID :one
SELECT sqlc.embed(j) FROM moderation_jobs j WHERE j.id = sqlc.arg(id);

-- name: GetJobViewsForReviewer :many
SELECT sqlc.embed(j) FROM moderation_jobs j
WHERE reviewer_id = sqlc.arg(reviewer_id)
AND moderation_request_id = ANY(sqlc.slice(moderation_request_ids))
ORDER BY created_at DESC;
