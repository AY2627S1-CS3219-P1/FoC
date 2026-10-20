-- name: CreateAccountWarning :execrows
INSERT INTO account_warnings (
    user_id, request_id, report_id, reason, status, source_event_id,
    created_by, updated_by
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (source_event_id) DO NOTHING;

-- name: GetAccountWarningByEventID :one
SELECT id, user_id, request_id, report_id, reason, status, source_event_id,
       removed_at, removed_reason, appeal_id, created_at, updated_at,
       deleted_at, created_by, updated_by
FROM account_warnings
WHERE source_event_id = $1;

-- name: GetAccountWarningByID :one
SELECT id, user_id, request_id, report_id, reason, status, source_event_id,
       removed_at, removed_reason, appeal_id, created_at, updated_at,
       deleted_at, created_by, updated_by
FROM account_warnings
WHERE id = $1 AND deleted_at IS NULL;

-- name: ListAccountWarningsByUser :many
SELECT id, user_id, request_id, report_id, reason, status, source_event_id,
       removed_at, removed_reason, appeal_id, created_at, updated_at,
       deleted_at, created_by, updated_by
FROM account_warnings
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC;

-- name: RemoveAccountWarning :one
UPDATE account_warnings
SET status = 'removed', removed_at = $2, removed_reason = $3,
    appeal_id = $4, updated_by = $5, updated_at = $2
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
RETURNING id, user_id, request_id, report_id, reason, status, source_event_id,
          removed_at, removed_reason, appeal_id, created_at, updated_at,
          deleted_at, created_by, updated_by;
