-- name: EnqueueNotificationEmail :exec
INSERT INTO notification_emails (event_id, user_id, body, created_at, available_at, expires_at, order_id, order_revision)
SELECT sqlc.arg(event_id), sqlc.arg(user_id), sqlc.arg(body), sqlc.arg(created_at), sqlc.arg(created_at),
       sqlc.narg(expires_at), sqlc.narg(order_id), sqlc.narg(order_revision)
WHERE NOT EXISTS (
    SELECT 1 FROM order_notifications_sent
    WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id)
)
ON CONFLICT DO NOTHING;

-- name: NextNotificationEmail :one
-- Unknown emails never block ready recipients. Keep the row locked through
-- the bounded SMTP attempt, so another worker cannot send the same row.
SELECT n.event_id, n.user_id, n.body, COALESCE(u.email, '')::text AS email, validity.skip_reason
FROM notification_emails n
LEFT JOIN user_emails u ON u.user_id = n.user_id
CROSS JOIN LATERAL (
    SELECT CASE
        WHEN n.expires_at < sqlc.arg(now)::timestamptz THEN 'notification expired'
        WHEN n.order_id IS NOT NULL AND NOT EXISTS (
            SELECT 1 FROM order_participants p WHERE p.order_id = n.order_id AND p.revision <= n.order_revision
        ) THEN 'order superseded'
        ELSE ''
    END::text AS skip_reason
) validity
WHERE n.status = 'pending' AND n.available_at <= sqlc.arg(now)::timestamptz
  AND (u.user_id IS NOT NULL OR validity.skip_reason <> '')
ORDER BY n.available_at, n.created_at, n.event_id, n.user_id
LIMIT 1
FOR UPDATE OF n SKIP LOCKED;

-- name: DeleteNotificationEmail :exec
DELETE FROM notification_emails
WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id);

-- name: SkipNotificationEmail :exec
UPDATE notification_emails
SET status = 'skipped', attempted_at = sqlc.arg(attempted_at), last_error = sqlc.arg(last_error)
WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id);

-- name: RetryNotificationEmail :exec
UPDATE notification_emails
SET available_at = sqlc.arg(available_at), attempts = attempts + 1,
    attempted_at = sqlc.arg(attempted_at), last_error = sqlc.arg(last_error)
WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id);

-- name: RejectNotificationEmail :exec
UPDATE notification_emails
SET status = 'rejected', attempts = attempts + 1,
    attempted_at = sqlc.arg(attempted_at), last_error = sqlc.arg(last_error)
WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id);
