-- name: CreateAccountWarning :execrows
INSERT INTO account_warnings (
    user_id, request_id, report_id, reason, status, source_event_id,
    created_by, updated_by, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE(sqlc.narg(created_at)::timestamptz, now()))
ON CONFLICT (source_event_id) DO NOTHING;

-- name: ListAccountWarningsByUser :many
SELECT id, user_id, request_id, report_id, reason, status, source_event_id,
       removed_at, removed_reason, appeal_id, created_at, updated_at,
       deleted_at, created_by, updated_by
FROM account_warnings
WHERE user_id = $1 AND deleted_at IS NULL
ORDER BY created_at DESC, id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: ListAccountWarningsForAdmin :many
-- The admin read view additionally joins the display name of the user who last
-- updated the warning; system-created warnings have no updater, so the join is
-- optional and the name is nullable.
SELECT w.id, w.user_id, w.request_id, w.report_id, w.reason, w.status, w.source_event_id,
       w.removed_at, w.removed_reason, w.appeal_id, w.created_at, w.updated_at,
       w.deleted_at, w.created_by, w.updated_by,
       updater.display_name AS updated_by_name
FROM account_warnings w
LEFT JOIN users updater ON updater.id = w.updated_by
WHERE w.user_id = $1 AND w.deleted_at IS NULL
ORDER BY w.created_at DESC, w.id DESC
LIMIT @page_limit::bigint OFFSET @page_offset::bigint;

-- name: CountAccountWarningsByUser :one
SELECT COUNT(*)
FROM account_warnings
WHERE user_id = $1 AND deleted_at IS NULL;

-- name: RemoveAccountWarning :one
UPDATE account_warnings
SET status = 'removed', removed_at = $2, removed_reason = $3,
    appeal_id = $4, updated_by = $5, updated_at = $2
WHERE id = $1 AND status = 'active' AND deleted_at IS NULL
RETURNING id, user_id, request_id, report_id, reason, status, source_event_id,
          removed_at, removed_reason, appeal_id, created_at, updated_at,
          deleted_at, created_by, updated_by;
