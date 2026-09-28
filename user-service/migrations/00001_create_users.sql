-- User profiles. Email is unique among rows that are not soft-deleted.
-- +goose Up
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                  BIGSERIAL PRIMARY KEY,
    email               CITEXT NOT NULL,
    display_name        TEXT NOT NULL CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 50),
    description         TEXT NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    telegram_handle     TEXT CHECK (telegram_handle ~ '^[A-Za-z0-9_]{5,32}$'),
    phone_number        TEXT CHECK (char_length(phone_number) <= 20),
    profile_picture_key TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    deleted_at          TIMESTAMPTZ,
    created_by          BIGINT REFERENCES users (id) ON DELETE SET NULL,
    updated_by          BIGINT REFERENCES users (id) ON DELETE SET NULL
);

CREATE UNIQUE INDEX idx_users_email_live ON users (email) WHERE deleted_at IS NULL;
CREATE INDEX idx_users_created_at ON users (created_at);

-- +goose Down
DROP TABLE IF EXISTS users;
DROP EXTENSION IF EXISTS citext;
