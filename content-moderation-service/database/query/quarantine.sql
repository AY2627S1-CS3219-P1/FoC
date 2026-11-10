-- name: Quarantine :exec
INSERT INTO moderation_quarantine (topic, message_key, event_id, reason, payload_summary)
VALUES (
    sqlc.arg(topic), sqlc.arg(message_key), sqlc.narg(event_id),
    sqlc.arg(reason), sqlc.narg(payload_summary)
);
