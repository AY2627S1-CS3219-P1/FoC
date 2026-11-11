-- Superadmins created by the admin bootstrap before it queued a user-created
-- event never got a Credit account. Queue the missing event once; the outbox
-- relay publishes it and Credit opens the account with the starting credits.

-- +goose Up
INSERT INTO user_created_outbox (event_id, user_id, email, display_name, user_created_at, payload_version)
SELECT gen_random_uuid(), u.id, u.email, u.display_name, u.created_at, 1
FROM admin_bootstrap b
JOIN users u ON u.id = b.user_id AND u.deleted_at IS NULL
ON CONFLICT (user_id) DO NOTHING;

-- +goose Down
-- Queued or published events are kept: Credit may already have opened the account.
SELECT 1;
