-- name: SaveOrderParticipants :exec
-- Older or repeated revisions change nothing, so replays and late events
-- can't restore an old courier.
INSERT INTO order_participants
    (order_id, requester_id, courier_id, status, revision, pickup_location_name, ended_at, updated_at)
VALUES (sqlc.arg(order_id), sqlc.arg(requester_id), sqlc.narg(courier_id), sqlc.arg(status), sqlc.arg(revision),
        sqlc.arg(pickup_location_name), sqlc.narg(ended_at), sqlc.arg(updated_at))
ON CONFLICT (order_id) DO UPDATE SET
    requester_id = EXCLUDED.requester_id, courier_id = EXCLUDED.courier_id,
    status = EXCLUDED.status, revision = EXCLUDED.revision,
    pickup_location_name = EXCLUDED.pickup_location_name,
    ended_at = EXCLUDED.ended_at, updated_at = EXCLUDED.updated_at
WHERE order_participants.revision < EXCLUDED.revision;

-- name: GetOrderParticipants :one
SELECT order_id, requester_id, courier_id, status, revision, pickup_location_name, ended_at
FROM order_participants WHERE order_id = sqlc.arg(order_id);

-- name: ListUserOrders :many
-- Apply chat eligibility before the keyboard limit so closed or unassigned
-- orders cannot hide a user's open orders.
SELECT order_id, requester_id, courier_id, status, revision, pickup_location_name, ended_at
FROM order_participants
WHERE (requester_id = sqlc.arg(user_id) OR courier_id = sqlc.arg(user_id))
  AND courier_id IS NOT NULL
  AND (status IN ('awaiting_pickup', 'delivering', 'delivered')
       OR (status IN ('completed', 'cancelled', 'failed')
           AND ended_at > sqlc.arg(since)::timestamptz))
ORDER BY updated_at DESC
LIMIT 20;

-- name: GetReplyTarget :one
SELECT order_id FROM chat_reply_targets WHERE chat_id = sqlc.arg(chat_id) AND message_id = sqlc.arg(message_id);

-- name: SaveReplyTarget :exec
INSERT INTO chat_reply_targets (chat_id, message_id, order_id, created_at)
VALUES (sqlc.arg(chat_id), sqlc.arg(message_id), sqlc.arg(order_id), sqlc.arg(created_at))
ON CONFLICT (chat_id, message_id) DO UPDATE SET order_id = EXCLUDED.order_id, created_at = EXCLUDED.created_at;

-- name: GetSelection :one
SELECT order_id FROM chat_selections WHERE user_id = sqlc.arg(user_id);

-- name: SaveSelection :exec
INSERT INTO chat_selections (user_id, order_id, selected_at)
VALUES (sqlc.arg(user_id), sqlc.arg(order_id), sqlc.arg(selected_at))
ON CONFLICT (user_id) DO UPDATE SET order_id = EXCLUDED.order_id, selected_at = EXCLUDED.selected_at;

-- name: SaveChatMessage :exec
INSERT INTO chat_messages (id, order_id, sender_id, recipient_id, body, sent_at)
VALUES (sqlc.arg(id), sqlc.arg(order_id), sqlc.arg(sender_id), sqlc.arg(recipient_id), sqlc.arg(body), sqlc.arg(sent_at))
ON CONFLICT (id) DO NOTHING;

-- name: DeleteExpiredChatMessages :execrows
DELETE FROM chat_messages m
WHERE EXISTS (SELECT 1 FROM order_participants p
              WHERE p.order_id = m.order_id AND p.ended_at < sqlc.arg(cutoff)::timestamptz)
   OR (m.sent_at < sqlc.arg(cutoff)::timestamptz
       AND NOT EXISTS (SELECT 1 FROM order_participants p WHERE p.order_id = m.order_id));

-- name: DeleteExpiredReplyTargets :exec
-- Eligible active targets survive regardless of age; terminal targets use
-- ended_at. Orphan and ineligible targets expire by their creation time.
DELETE FROM chat_reply_targets t
WHERE t.order_id IN (SELECT p.order_id FROM order_participants p WHERE p.ended_at < sqlc.arg(cutoff)::timestamptz)
   OR (t.created_at < sqlc.arg(cutoff)::timestamptz
       AND NOT EXISTS (
           SELECT 1 FROM order_participants p
           WHERE p.order_id = t.order_id
             AND (p.ended_at IS NOT NULL
                  OR (p.courier_id IS NOT NULL
                      AND p.status IN ('awaiting_pickup', 'delivering', 'delivered')))));

-- name: DeleteExpiredNotificationRecords :exec
DELETE FROM order_notifications_sent WHERE sent_at < sqlc.arg(cutoff)::timestamptz;

-- name: DeleteClosedSelections :exec
DELETE FROM chat_selections s
WHERE NOT EXISTS (SELECT 1 FROM order_participants p WHERE p.order_id = s.order_id)
   OR s.order_id IN (SELECT p.order_id FROM order_participants p WHERE p.ended_at <= sqlc.arg(closed_before)::timestamptz);

-- name: DeleteExpiredOrderParticipants :exec
DELETE FROM order_participants WHERE ended_at < sqlc.arg(cutoff)::timestamptz;
