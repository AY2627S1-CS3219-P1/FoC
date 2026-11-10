-- name: NotifyChanges :exec
SELECT pg_notify('foc_realtime',sqlc.arg(payload)::text);
