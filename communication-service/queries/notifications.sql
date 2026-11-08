-- name: NotificationSent :one
SELECT EXISTS (
    SELECT 1 FROM order_notifications_sent WHERE event_id = sqlc.arg(event_id) AND user_id = sqlc.arg(user_id)
);

-- name: RecordNotificationSent :exec
-- Recording twice is harmless.
INSERT INTO order_notifications_sent (event_id, user_id, sent_at)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), sqlc.arg(sent_at))
ON CONFLICT DO NOTHING;

-- name: RecordNotificationsStart :exec
-- Only the first call sets the start time.
INSERT INTO notifications_start (started_at) VALUES (sqlc.arg(started_at))
ON CONFLICT DO NOTHING;

-- name: NotificationsStart :one
SELECT started_at FROM notifications_start;
