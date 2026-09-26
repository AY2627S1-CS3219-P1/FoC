CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE users (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email               CITEXT      NOT NULL UNIQUE,
    display_name        VARCHAR(50) NOT NULL,
    description         VARCHAR(500) NOT NULL DEFAULT '',
    telegram_handle     VARCHAR(32),
    phone_number        VARCHAR(20),
    profile_picture_key TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_created_at ON users (created_at);
