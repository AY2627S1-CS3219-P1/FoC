-- name: GetOperationalStats :one
SELECT
    (SELECT count(*) FROM moderation_jobs WHERE status IN ('queued', 'retry_wait', 'processing')) AS queue_depth,
    COALESCE((SELECT min(created_at) FROM moderation_jobs WHERE status IN ('queued', 'retry_wait', 'processing')), now()) AS oldest_job_at,
    (SELECT count(*) FROM moderation_jobs WHERE status = 'errored') AS exhausted_jobs,
    (SELECT count(*) FROM moderation_outbox WHERE published_at IS NULL) AS pending_outbox,
    COALESCE((SELECT min(created_at) FROM moderation_outbox WHERE published_at IS NULL), now()) AS oldest_outbox_at;
