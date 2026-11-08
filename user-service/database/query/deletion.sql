-- Deletion-job and user-deleted-outbox persistence, owned by the account
-- deletion worker. All statements are transactional so a failed finalization
-- rolls back with the rest of the work and never leaves a half-deleted account.

-- Insert the single pending-work job at deletion acceptance.
-- name: InsertJob :exec
INSERT INTO account_deletion_jobs
    (user_id, deletion_request_id, admissions_safe_after, next_attempt_at)
VALUES
    (@user_id::UUID, @deletion_request_id::UUID, @admissions_safe_after::TIMESTAMPTZ, @next_attempt_at::TIMESTAMPTZ);

-- Claim one due job at a time with a lease. FOR UPDATE SKIP LOCKED keeps
-- replicas from processing the same row; a job past its lease becomes claimable
-- again. attempt_count advances so retries are observable. Returns the captured
-- row for the drain check or the finalization recheck.
-- name: ClaimDueJob :one
WITH candidate AS (
    SELECT user_id
    FROM account_deletion_jobs
    WHERE next_attempt_at IS NOT NULL
      AND next_attempt_at <= now()
      AND (lease_until IS NULL OR lease_until <= now())
    ORDER BY next_attempt_at, user_id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE account_deletion_jobs AS job
    SET lease_token = @lease_token::UUID,
        lease_until = @lease_until::TIMESTAMPTZ,
        attempt_count = attempt_count + 1
    FROM candidate
    WHERE job.user_id = candidate.user_id
    RETURNING job.user_id,
              job.deletion_request_id,
              job.admissions_safe_after,
              job.next_attempt_at,
              job.last_checked_at,
              job.attempt_count,
              job.lease_token,
              job.lease_until,
              job.waiting_reasons,
              job.last_error;

-- Record a drain check after a postponing result: the bounded waiting reasons
-- and classified last error, a scheduled next attempt, and the released lease so
-- a later poll or another replica can retry. The lease guard stops a stale
-- replica from clearing a lease another replica is mid-flight on.
-- name: RecordPendingCheck :exec
UPDATE account_deletion_jobs
    SET last_checked_at = @last_checked_at::TIMESTAMPTZ,
        waiting_reasons = @waiting_reasons::text[],
        last_error = @last_error,
        next_attempt_at = @next_attempt_at::TIMESTAMPTZ,
        lease_token = NULL,
        lease_until = NULL
    WHERE user_id = @user_id::UUID
      AND lease_token = @lease_token::UUID;

-- Finalize-lock reclaims the job under the worker's lease, refreshing the lease
-- so it covers the whole hard-delete. It returns the row only while the current
-- lease still holds, so a stale worker whose lease was lost to another replica
-- finalizes nothing. A job whose users row is already gone cascades away and
-- yields no row, so double finalizations never run.
-- name: LockJobForFinalize :one
UPDATE account_deletion_jobs
    SET lease_until = @lease_until::TIMESTAMPTZ
    WHERE user_id = @user_id::UUID
      AND lease_token = @lease_token::UUID
    RETURNING user_id, deletion_request_id, admissions_safe_after, last_checked_at,
              attempt_count, lease_token, lease_until, waiting_reasons, last_error;

-- Read-only job status for the account's own deletion-status query. No lock and
-- no lease: an account only ever inspects its own pending job.
-- name: GetDeletionJob :one
SELECT user_id, deletion_request_id, admissions_safe_after, next_attempt_at,
       last_checked_at, attempt_count, waiting_reasons, last_error
FROM account_deletion_jobs
WHERE user_id = @user_id::UUID;

-- Remove the job row in the final hard-delete transaction (the FK also cascades).
-- name: DeleteJob :exec
DELETE FROM account_deletion_jobs WHERE user_id = @user_id::UUID;

-- Insert exactly one committed deletion fact. The UNIQUE constraints on user_id
-- and deletion_request_id enforce a single fact per account even under a
-- concurrent worker.
-- name: InsertDeletedFact :exec
INSERT INTO user_deleted_outbox
    (event_id, user_id, deletion_request_id, deleted_at, payload_version, created_at, available_at)
VALUES
    (@event_id::UUID, @user_id::UUID, @deletion_request_id::UUID,
     @deleted_at::TIMESTAMPTZ, 1, now(), now());

-- Claim a committed, unpublished deletion fact for publication with a lease.
-- name: ClaimCommittedFact :one
WITH candidate AS (
    SELECT event_id
    FROM user_deleted_outbox
    WHERE published_at IS NULL
      AND available_at <= now()
      AND (lease_until IS NULL OR lease_until <= now())
    ORDER BY created_at, event_id
    LIMIT 1
    FOR UPDATE SKIP LOCKED
)
UPDATE user_deleted_outbox AS fact
    SET lease_token = @lease_token::UUID,
        lease_until = @lease_until::TIMESTAMPTZ
    FROM candidate
    WHERE fact.event_id = candidate.event_id
    RETURNING fact.event_id,
              fact.user_id,
              fact.deletion_request_id,
              fact.deleted_at,
              fact.payload_version,
              fact.lease_token;

-- Mark a fact published after the broker acknowledges it, only while the current
-- lease still holds. A lost lease means another relay already took it over.
-- name: MarkFactPublished :exec
UPDATE user_deleted_outbox
    SET published_at = @published_at::TIMESTAMPTZ,
        lease_token = NULL,
        lease_until = NULL
    WHERE event_id = @event_id::UUID
      AND lease_token = @lease_token::UUID
      AND lease_until > @lease_now::TIMESTAMPTZ
      AND published_at IS NULL;

-- Schedule a durable retry for a fact whose publication failed, keeping the same
-- event id and dropping the stale lease.
-- name: RetryFact :exec
UPDATE user_deleted_outbox
    SET available_at = @available_at::TIMESTAMPTZ,
        lease_token = NULL,
        lease_until = NULL
    WHERE event_id = @event_id::UUID
      AND lease_token = @lease_token::UUID
      AND published_at IS NULL;
