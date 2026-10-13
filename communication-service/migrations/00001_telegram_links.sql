-- +goose Up
-- One Telegram chat per FoC account, and one account per chat.
CREATE TABLE telegram_links (
    user_id           uuid PRIMARY KEY,
    chat_id           bigint NOT NULL UNIQUE,
    telegram_username text,
    linked_at         timestamptz NOT NULL
);

-- One-time codes from CreateTelegramLink. Only the SHA-256 hash is stored.
CREATE TABLE telegram_link_codes (
    code_hash  bytea PRIMARY KEY CHECK (octet_length(code_hash) = 32),
    user_id    uuid NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL
);

CREATE INDEX telegram_link_codes_unused_by_user
    ON telegram_link_codes (user_id) WHERE used_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS telegram_link_codes;
DROP TABLE IF EXISTS telegram_links;
